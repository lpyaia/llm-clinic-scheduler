"""Regenerate the small, deterministic simulated clinic PDFs (requires reportlab)."""

from pathlib import Path

from reportlab.lib.colors import HexColor
from reportlab.lib.pagesizes import A4
from reportlab.pdfgen import canvas


ROOT = Path(__file__).resolve().parents[1] / "docs" / "knowledge"


def write_pdf(filename, title, pages):
    output = canvas.Canvas(str(ROOT / filename), pagesize=A4, invariant=1, pageCompression=1)
    output.setTitle(title)
    output.setAuthor("Clínica Horizonte - cenário fictício")

    for page_number, lines in enumerate(pages, 1):
        output.setFillColor(HexColor("#176B75"))
        output.rect(0, 760, A4[0], 82, fill=1, stroke=0)
        output.setFillColor(HexColor("#FFFFFF"))
        output.setFont("Helvetica-Bold", 16)
        output.drawString(48, 795, title)
        output.setFillColor(HexColor("#223344"))
        output.setFont("Helvetica", 11)
        output.drawString(48, 730, "Material fictício para demonstração. Não usar como orientação clínica real.")

        for index, line in enumerate(lines):
            output.drawString(48, 680 - index * 24, line)

        output.setFont("Helvetica", 9)
        output.drawString(48, 42, f"Clínica Horizonte simulada | Página {page_number}")
        output.showPage()

    output.save()


write_pdf(
    "preparo-ultrassom-abdominal-simulated.pdf",
    "Preparo - ultrassom abdominal",
    [
        [
            "Retire o formulário azul na recepção antes do ultrassom abdominal.",
            "Apresente o pedido e o documento de identificação no balcão de exames.",
            "A recepção confere as instruções individuais fornecidas pela equipe responsável.",
            "Este material não define jejum, medicamentos ou decisões clínicas.",
        ],
        [
            "Entregue o formulário azul preenchido na sala de apoio 2.",
            "Dúvidas sobre o preparo individual devem ser esclarecidas com a equipe responsável.",
        ],
    ],
)

write_pdf(
    "preparo-ultrassom-tireoide-simulated.pdf",
    "Preparo - ultrassom da tireoide",
    [
        [
            "Retire o formulário verde na recepção antes do ultrassom da tireoide.",
            "Entregue o formulário verde na sala de apoio 4.",
            "Este material não define jejum, medicamentos ou decisões clínicas.",
        ]
    ],
)
