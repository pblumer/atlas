# ISDS-Konzept → Word

Renders [`docs/compliance/isds-konzept.md`](../../docs/compliance/isds-konzept.md)
into the BIT/NCSC Word template **P042-Hi01**, so the version that gets handed to
an ISBO is generated from the markdown rather than maintained beside it.

The template itself is **not** in this repository — it is a BIT document. Obtain
the current `P042-Hi01 ISDS_Konzept` template and pass it in.

```bash
pip install python-docx
python3 scripts/isds-docx/render.py \
    docs/compliance/isds-konzept.md \
    ~/P042-Hi01_ISDS_Konzept_BIT-Template.docx \
    ISDS-Konzept_Atlas.docx
```

What it does:

- keeps the template's cover page, change-control and approval tables, headers,
  footers and styles, and fills the cover fields it knows (classification,
  status, version, date, author, subtitle);
- replaces the body from the first chapter heading onwards with the converted
  markdown — headings map onto the template's auto-numbered `Heading 1..3`, so
  the chapter numbers come from Word, not from the text;
- converts tables to `TableGrid`, keeps bold and inline code, and drops fenced
  code blocks in as monospaced lines.

Afterwards, in Word: update the table of contents (Ctrl+A, F9) and replace the
Mermaid source in 5.4.1 with an actual drawing.
