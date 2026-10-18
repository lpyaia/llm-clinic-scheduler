//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lpyaia/llm-clinic-scheduler/internal/adapters/httpapi"
	"github.com/lpyaia/llm-clinic-scheduler/internal/application"
	"github.com/lpyaia/llm-clinic-scheduler/internal/domain"
	"github.com/lpyaia/llm-clinic-scheduler/internal/timeouts"
)

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

type fixture struct {
	t                              *testing.T
	store                          *Store
	service                        *application.Service
	clock                          *testClock
	doctor, otherDoctor, specialty uuid.UUID
	dsn                            string
}

func setup(t *testing.T) *fixture {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("set TEST_DATABASE_URL to a disposable PostgreSQL database URL")
	}

	base, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := base.Close(); err != nil {
			t.Error(err)
		}
	})

	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := base.db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := base.db.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	})

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}

	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	store, err := Open(u.String())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}

	c := &testClock{now: time.Date(2026, 10, 19, 8, 0, 0, 0, loc)}
	f := &fixture{t: t, store: store, clock: c, doctor: uuid.New(), otherDoctor: uuid.New(), specialty: uuid.New(), dsn: u.String()}
	f.service = &application.Service{Repo: store, Clock: c, Location: loc}
	f.must(store.db.Create(&specialtyRow{ID: f.specialty, Name: "Cardiologia"}).Error)
	f.must(store.db.Create(&doctorRow{ID: f.doctor, Name: "Dra. Ana Silva", SpecialtyID: f.specialty}).Error)
	f.must(store.db.Create(&doctorRow{ID: f.otherDoctor, Name: "Dr. Bruno Santos", SpecialtyID: f.specialty}).Error)

	return f
}
func (f *fixture) must(err error) {
	f.t.Helper()

	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) patient() (domain.Principal, string) {
	f.t.Helper()

	phone := "+5511" + fmt.Sprintf("%09d", time.Now().UnixNano()%1000000000)
	out, err := f.service.StartSession(context.Background(), "Paciente de teste", phone)
	f.must(err)

	return domain.Principal{PatientID: out.Patient.ID}, out.Token
}
func (f *fixture) slot(day, hour int) uuid.UUID { return f.doctorSlot(f.doctor, day, hour) }
func (f *fixture) doctorSlot(doctor uuid.UUID, day, hour int) uuid.UUID {
	f.t.Helper()

	id := uuid.New()
	start := f.clock.now.AddDate(0, 0, day)
	start = time.Date(start.Year(), start.Month(), start.Day(), hour, 0, 0, 0, start.Location())
	f.must(f.store.db.Create(&slotRow{ID: id, DoctorID: doctor, StartsAt: start}).Error)

	return id
}
func (f *fixture) prepare(p domain.Principal, in application.PrepareInput) domain.Action {
	f.t.Helper()
	a, err := f.service.Prepare(context.Background(), p, in)
	f.must(err)

	return a
}
func (f *fixture) prepareCreate(p domain.Principal, slot uuid.UUID) domain.Action {
	return f.prepare(p, application.PrepareInput{Operation: domain.CreateAppointment, SlotID: slot, Category: "consultation"})
}
func (f *fixture) book(p domain.Principal, slot uuid.UUID) domain.Appointment {
	f.t.Helper()
	a := f.prepareCreate(p, slot)
	out, err := f.service.Confirm(context.Background(), p, a.ID)
	f.must(err)

	return out
}
func code(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil || domain.ErrorCode(err) != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func TestConcurrentSlotBooking(t *testing.T) {
	f := setup(t)
	p1, _ := f.patient()
	p2, _ := f.patient()
	slot := f.slot(1, 14)
	a1, a2 := f.prepareCreate(p1, slot), f.prepareCreate(p2, slot)
	start := make(chan struct{})
	results := make(chan error, 2)

	for _, v := range []struct {
		p domain.Principal
		a domain.Action
	}{{p1, a1}, {p2, a2}} {
		go func() { <-start; _, err := f.service.Confirm(context.Background(), v.p, v.a.ID); results <- err }()
	}

	close(start)

	success, conflict := 0, 0

	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if domain.ErrorCode(err) == "SLOT_UNAVAILABLE" {
			conflict++
		} else {
			t.Fatal(err)
		}
	}

	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}

	var count int64
	f.must(f.store.db.Model(&appointmentRow{}).Where("slot_id = ?", slot).Count(&count).Error)

	if count != 1 {
		t.Fatalf("bookings=%d", count)
	}
	// The unique index also protects writes that bypass application validation.
	err := f.store.InsertAppointment(context.Background(), domain.Appointment{ID: uuid.New(), PatientID: p2.PatientID, SlotID: slot, Category: "exam", Status: domain.Scheduled, Version: 1})
	code(t, err, "SLOT_UNAVAILABLE")
}

func TestConcurrentWeeklyLimit(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	f.book(p, f.slot(1, 9))
	f.book(p, f.slot(1, 10))
	a1, a2 := f.prepareCreate(p, f.slot(2, 9)), f.prepareCreate(p, f.slot(2, 10))
	start := make(chan struct{})
	results := make(chan error, 2)

	for _, a := range []domain.Action{a1, a2} {
		go func() { <-start; _, err := f.service.Confirm(context.Background(), p, a.ID); results <- err }()
	}

	close(start)

	success := 0

	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else {
			code(t, err, "WEEKLY_LIMIT_REACHED")
		}
	}

	if success != 1 {
		t.Fatalf("success=%d", success)
	}

	rows, err := f.service.ListAppointments(context.Background(), p, application.AppointmentFilter{})
	f.must(err)

	if len(rows) != 3 {
		t.Fatalf("bookings=%d", len(rows))
	}
}

func TestConfirmationLifecycleAndOwnership(t *testing.T) {
	f := setup(t)
	p, token := f.patient()
	other, _ := f.patient()
	a := f.prepareCreate(p, f.slot(1, 14))
	rows, err := f.service.ListAppointments(context.Background(), p, application.AppointmentFilter{})
	f.must(err)

	if len(rows) != 0 {
		t.Fatal("prepare reserved slot")
	}

	_, err = f.service.GetAction(context.Background(), other, a.ID)
	code(t, err, "NOT_FOUND")
	_, err = f.service.Confirm(context.Background(), other, a.ID)
	code(t, err, "NOT_FOUND")

	var wg sync.WaitGroup

	results := make(chan domain.Appointment, 2)
	errs := make(chan error, 2)

	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			out, err := f.service.Confirm(context.Background(), p, a.ID)
			results <- out

			errs <- err
		}()
	}

	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		f.must(err)
	}

	id := uuid.Nil
	for out := range results {
		if id != uuid.Nil && out.ID != id {
			t.Fatal("duplicate booking")
		}

		id = out.ID
	}

	_, err = f.service.Prepare(context.Background(), other, application.PrepareInput{Operation: domain.CancelAppointment, AppointmentID: id})
	code(t, err, "NOT_FOUND")
	// A new connection and service retain identity, action, and original response.
	reopened, err := Open(f.dsn)
	f.must(err)

	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()

	service := &application.Service{Repo: reopened, Clock: f.clock, Location: f.service.Location}
	principal, err := service.Authenticate(context.Background(), token)
	f.must(err)

	if principal.PatientID != p.PatientID || principal.IdentityVerified {
		t.Fatal("identity not preserved")
	}

	out, err := service.Confirm(context.Background(), principal, a.ID)
	f.must(err)

	if out.ID != id {
		t.Fatal("result not preserved")
	}

	expiring := f.prepareCreate(p, f.slot(2, 14))
	f.clock.now = f.clock.now.Add(10 * time.Minute)
	_, err = service.Confirm(context.Background(), p, expiring.ID)
	code(t, err, "ACTION_EXPIRED")
	stored, err := service.GetAction(context.Background(), p, expiring.ID)
	f.must(err)

	if stored.Status != domain.Failed {
		t.Fatal("expiration not persisted")
	}

	f.clock.now = f.clock.now.Add(24 * time.Hour)
	_, err = service.Authenticate(context.Background(), token)
	code(t, err, "UNAUTHORIZED")
}

func TestRescheduleCancelAndWeeklyCounting(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	originalSlot := f.slot(1, 9)
	original := f.book(p, originalSlot)
	completed := f.book(p, f.slot(2, 9))
	f.must(f.store.db.Model(&appointmentRow{}).Where("id = ?", completed.ID).Update("status", domain.Completed).Error)
	f.book(p, f.slot(3, 9))
	_, err := f.service.Prepare(context.Background(), p, application.PrepareInput{Operation: domain.CreateAppointment, SlotID: f.slot(4, 9), Category: "exam"})
	code(t, err, "WEEKLY_LIMIT_REACHED")

	target := f.slot(1, 10)
	move := f.prepare(p, application.PrepareInput{Operation: domain.RescheduleAppointment, AppointmentID: original.ID, SlotID: target})
	staleCancel := f.prepare(p, application.PrepareInput{Operation: domain.CancelAppointment, AppointmentID: original.ID})
	moved, err := f.service.Confirm(context.Background(), p, move.ID)
	f.must(err)

	if moved.ID != original.ID || moved.SlotID != target || moved.Category != original.Category || moved.DoctorID != original.DoctorID {
		t.Fatalf("bad reschedule: %+v", moved)
	}

	occupied, err := f.store.SlotOccupied(context.Background(), originalSlot, uuid.Nil)
	f.must(err)

	if occupied {
		t.Fatal("reschedule did not release original slot")
	}

	_, err = f.service.Confirm(context.Background(), p, staleCancel.ID)
	code(t, err, "APPOINTMENT_CHANGED")
	_, err = f.service.Prepare(context.Background(), p, application.PrepareInput{Operation: domain.RescheduleAppointment, AppointmentID: moved.ID, SlotID: f.doctorSlot(f.otherDoctor, 1, 14)})
	code(t, err, "INVALID_INPUT")

	cancel := f.prepare(p, application.PrepareInput{Operation: domain.CancelAppointment, AppointmentID: moved.ID})
	cancelled, err := f.service.Confirm(context.Background(), p, cancel.ID)
	f.must(err)

	if cancelled.Status != domain.Cancelled {
		t.Fatal("not cancelled")
	}

	f.book(p, target) // Cancellation frees both its slot and the weekly limit.
	_, err = f.service.Prepare(context.Background(), p, application.PrepareInput{Operation: domain.CancelAppointment, AppointmentID: completed.ID})
	code(t, err, "APPOINTMENT_NOT_MUTABLE")
}

func TestFailedReschedulePreservesOriginal(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	other, _ := f.patient()
	original := f.book(p, f.slot(1, 9))
	target := f.slot(2, 9)
	move := f.prepare(p, application.PrepareInput{Operation: domain.RescheduleAppointment, AppointmentID: original.ID, SlotID: target})
	f.book(other, target)
	_, err := f.service.Confirm(context.Background(), p, move.ID)
	code(t, err, "SLOT_UNAVAILABLE")
	unchanged, err := f.store.Appointment(context.Background(), original.ID, p.PatientID, false)
	f.must(err)

	if unchanged.SlotID != original.SlotID || unchanged.Version != original.Version {
		t.Fatal("original changed")
	}

	stored, err := f.service.GetAction(context.Background(), p, move.ID)
	f.must(err)

	if stored.Status != domain.Failed {
		t.Fatal("failure not terminal")
	}

	_, err = f.service.Confirm(context.Background(), p, move.ID)
	code(t, err, "SLOT_UNAVAILABLE")
}

func TestRescheduleIntoFullWeek(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	original := f.book(p, f.slot(1, 9))

	target := f.slot(8, 14)
	move := f.prepare(p, application.PrepareInput{Operation: domain.RescheduleAppointment, AppointmentID: original.ID, SlotID: target})
	for day := 8; day <= 10; day++ {
		f.book(p, f.slot(day, 9))
	}

	_, err := f.service.Confirm(context.Background(), p, move.ID)
	code(t, err, "WEEKLY_LIMIT_REACHED")
	unchanged, err := f.store.Appointment(context.Background(), original.ID, p.PatientID, false)
	f.must(err)

	if unchanged.SlotID != original.SlotID {
		t.Fatal("original released")
	}
}

func TestRollbackAndTransientRetry(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	slot := f.slot(1, 9)
	a := f.prepareCreate(p, slot)
	// Inject a database write failure after the appointment INSERT. The entire
	// confirmation must roll back, leaving the action pending and retryable.
	f.must(f.store.db.Exec(`CREATE FUNCTION reject_action_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected transient failure'; END; $$`).Error)
	f.must(f.store.db.Exec(`CREATE TRIGGER fail_action BEFORE UPDATE ON pending_actions FOR EACH ROW EXECUTE FUNCTION reject_action_update()`).Error)

	_, err := f.service.Confirm(context.Background(), p, a.ID)
	if err == nil {
		t.Fatal("expected injected failure")
	}

	rows, err := f.service.ListAppointments(context.Background(), p, application.AppointmentFilter{})
	f.must(err)

	if len(rows) != 0 {
		t.Fatal("partial commit")
	}

	stored, err := f.service.GetAction(context.Background(), p, a.ID)
	f.must(err)

	if stored.Status != domain.Pending {
		t.Fatal("transient failure became terminal")
	}

	f.must(f.store.db.Exec("DROP TRIGGER fail_action ON pending_actions").Error)
	_, err = f.service.Confirm(context.Background(), p, a.ID)
	f.must(err)
	// Explicit transaction rollback also applies to repositories from the UoW.
	sentinel := errors.New("rollback")
	err = f.store.Transaction(context.Background(), func(r application.Repository) error {
		if err := r.InsertPatient(context.Background(), domain.Patient{ID: uuid.New(), FullName: "Rollback", Phone: "+5511888888888"}); err != nil {
			return err
		}

		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}

	_, err = f.store.PatientByPhone(context.Background(), "+5511888888888")
	code(t, err, "NOT_FOUND")
}

func TestSearchFiltersAndBoundaries(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	morning := f.slot(1, 9)
	f.slot(1, 14)
	f.slot(1, 18)
	f.doctorSlot(f.otherDoctor, 1, 15)

	for _, test := range []struct {
		period string
		want   int
	}{{"morning", 1}, {"afternoon", 1}, {"evening", 1}, {"", 3}} {
		out, err := f.service.FindSlots(context.Background(), application.SearchInput{DoctorID: f.doctor, SpecialtyID: f.specialty, Period: test.period})
		f.must(err)

		if len(out.Slots) != test.want {
			t.Fatalf("%s: %d", test.period, len(out.Slots))
		}
	}

	f.book(p, morning)
	out, err := f.service.FindSlots(context.Background(), application.SearchInput{DoctorID: f.doctor, Period: "morning"})
	f.must(err)

	if len(out.Slots) != 0 || !out.Until.Equal(f.clock.now.AddDate(0, 0, 56)) {
		t.Fatal("reserved slot available or wrong horizon")
	}

	explicit, err := f.service.FindSlots(context.Background(), application.SearchInput{DoctorID: f.doctor, To: f.clock.now.Add(time.Hour)})
	f.must(err)

	if len(explicit.Slots) != 0 || explicit.Expanded {
		t.Fatal("explicit interval expanded")
	}
	// Intersect filters rather than widening to other doctors.
	wrong, err := f.service.FindSlots(context.Background(), application.SearchInput{DoctorID: f.doctor, SpecialtyID: uuid.New()})
	f.must(err)

	if len(wrong.Slots) != 0 {
		t.Fatal("specialty intersection ignored")
	}
	// PostgreSQL also rejects overlapping grid starts and invalid durations.
	start := f.clock.now.AddDate(0, 0, 3).Add(time.Minute)

	err = f.store.db.Create(&slotRow{ID: uuid.New(), DoctorID: f.doctor, StartsAt: start}).Error
	if err == nil {
		t.Fatal("unaligned slot accepted")
	}
}

func TestProgressiveSearchAgainstPostgres(t *testing.T) {
	f := setup(t)
	f.slot(22, 14)
	out, err := f.service.FindSlots(context.Background(), application.SearchInput{DoctorID: f.doctor})
	f.must(err)

	if !out.Expanded || len(out.Slots) != 1 || !out.Until.Equal(f.clock.now.AddDate(0, 0, 28)) {
		t.Fatalf("%+v", out)
	}
}

func TestMaintenanceAndSeed(t *testing.T) {
	f := setup(t)
	p, _ := f.patient()
	a := f.book(p, f.slot(0, 9))
	f.clock.now = f.clock.now.Add(90 * time.Minute)
	count, err := f.store.CompleteDue(context.Background(), f.clock.now)
	f.must(err)

	if count != 1 {
		t.Fatalf("completed=%d", count)
	}

	out, err := f.store.Appointment(context.Background(), a.ID, p.PatientID, false)
	f.must(err)

	if out.Status != domain.Completed || out.Version != 2 {
		t.Fatalf("%+v", out)
	}

	count, err = f.store.CompleteDue(context.Background(), f.clock.now)
	f.must(err)

	if count != 0 {
		t.Fatal("completion not idempotent")
	}
	// The simulated catalog is repeatable without duplicating slots.
	f.must(f.store.Seed(context.Background(), f.clock.now, f.service.Location))

	var first int64
	f.must(f.store.db.Model(&slotRow{}).Count(&first).Error)
	f.must(f.store.Seed(context.Background(), f.clock.now, f.service.Location))

	var second int64
	f.must(f.store.db.Model(&slotRow{}).Count(&second).Error)

	if first != second || first < 100 {
		t.Fatalf("seed %d -> %d", first, second)
	}
}

func TestHTTPFullLifecycle(t *testing.T) {
	f := setup(t)
	slot := f.slot(1, 14)
	next := f.slot(2, 14)
	app := httpapi.New(f.service, f.store.Ping, slog.New(slog.NewTextHandler(io.Discard, nil)), timeouts.DefaultHTTP)
	token := ""
	request := func(method, path, body string, status int) map[string]any {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := app.Test(req)
		f.must(err)

		defer func() {
			if err := resp.Body.Close(); err != nil {
				t.Error(err)
			}
		}()

		data, err := io.ReadAll(resp.Body)
		f.must(err)

		if resp.StatusCode != status {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
		}

		var out map[string]any
		f.must(json.Unmarshal(data, &out))

		return out
	}
	out := request("POST", "/v1/dev/sessions", `{"full_name":"Maria Souza","phone":"+5511999999999"}`, 201)
	token = out["token"].(string)

	search := request("GET", "/v1/slots?doctor_id="+f.doctor.String()+"&period=afternoon", "", 200)
	if len(search["slots"].([]any)) != 2 {
		t.Fatal(search)
	}

	action := request("POST", "/v1/pending-actions", fmt.Sprintf(`{"operation":"create_appointment","category":"exam","slot_id":"%s"}`, slot), 201)
	if !strings.Contains(action["summary"].(string), "Dra. Ana Silva") {
		t.Fatal("missing confirmation summary")
	}

	appointment := request("POST", "/v1/pending-actions/"+action["id"].(string)+"/confirm", "", 200)

	move := request("POST", "/v1/pending-actions", fmt.Sprintf(`{"operation":"reschedule_appointment","appointment_id":"%s","slot_id":"%s"}`, appointment["id"], next), 201)
	moved := request("POST", "/v1/pending-actions/"+move["id"].(string)+"/confirm", `{}`, 200)
	if moved["slot_id"] != next.String() || moved["category"] != "exam" {
		t.Fatal(moved)
	}

	cancel := request("POST", "/v1/pending-actions", fmt.Sprintf(`{"operation":"cancel_appointment","appointment_id":"%s"}`, appointment["id"]), 201)

	cancelled := request("POST", "/v1/pending-actions/"+cancel["id"].(string)+"/confirm", "", 200)
	if cancelled["status"] != "cancelled" {
		t.Fatal(cancelled)
	}

	request("GET", "/v1/pending-actions/"+cancel["id"].(string), "", 200)
}
