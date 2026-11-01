package agent

import _ "embed"

//go:embed schemas/list_appointments.json
var listSchema string

//go:embed schemas/appointment_answer.json
var answerSchema string

//go:embed schemas/find_available_slots.json
var findAvailableSlotsSchema string
