# Convert Book Formats

Convert epub, pdf, and other formats to .txt for book analysis.

## Dependencies

Install these tools for format conversion:

- **Calibre** (for epub): `brew install calibre` / `sudo apt-get install calibre`
- **Poppler** (for pdf): `brew install poppler` / `sudo apt-get install poppler-utils`
- **Pandoc** (alternative): `brew install pandoc` / `sudo apt-get install pandoc`

## Conversion Commands

### EPUB to TXT
```bash
# Using Calibre (recommended - best formatting)
ebook-convert input.epub output.txt

# Using Pandoc (alternative)
pandoc input.epub -t plain -o output.txt
```

### PDF to TXT
```bash
# Using pdftotext (from poppler)
pdftotext input.pdf output.txt

# Preserve layout (for formatted text)
pdftotext -layout input.pdf output.txt
```

### MOBI to TXT
```bash
ebook-convert input.mobi output.txt
```

### DOCX to TXT
```bash
pandoc input.docx -t plain -o output.txt
```

## Post-Conversion Cleanup

After conversion, you may want to clean artifacts:

```bash
# Remove excessive blank lines
sed '/^$/N;/^\n$/d' output.txt > output-clean.txt
```

## Troubleshooting

- **PDF extraction garbled**: Try `-layout` flag or use OCR tools for scanned PDFs
- **EPUB missing content**: Check if DRM-protected (requires removal first)
- **Encoding issues**: Add `--encoding=utf-8` to calibre commands
