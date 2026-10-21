import os
import sys

sys.path.insert(0, os.path.dirname(__file__))
import feature

# index shape written by older ws-init: Active + Quick Reference only
OLD_INDEX = """# Feature Index

## Active Features

| Feature | Status | Beta Flag | Builds On |
|---------|--------|-----------|-----------|
| [foo](foo/) | in_progress |  | none | - |

## Quick Reference

- notes
"""


def _write(tmp_path, text):
    p = tmp_path / "_index.md"
    p.write_text(text)
    return p


def test_complete_adds_missing_section_and_moves_row(tmp_path):
    p = _write(tmp_path, OLD_INDEX)

    assert feature.move_index_row(str(p), "foo", "complete", "none", "")

    text = p.read_text()
    assert text.count("[foo](foo/)") == 1
    completed = text.index("## Completed Features")
    assert text.index("## Active Features") < completed < text.index("## Quick Reference")
    assert "| [foo](foo/) | complete |  | none |" in text[completed:]
    assert "| Feature | Status | Beta Flag | Builds On |\n|" in text[completed:]
    assert "\n\n## Completed Features\n\n" in text
    assert "|\n\n## Quick Reference" in text


def test_pending_adds_section_with_paired_column(tmp_path):
    p = _write(tmp_path, OLD_INDEX)

    assert feature.move_index_row(str(p), "bar", "pending")

    text = p.read_text()
    pending = text.index("## Pending Features")
    assert "| Feature | Status | Beta Flag | Builds On | Paired |" in text[pending:]
    assert "| [bar](bar/) | pending |  | - | - |" in text[pending:]
    assert "[foo](foo/)" in text


def test_existing_section_is_reused(tmp_path):
    p = _write(tmp_path, OLD_INDEX)
    feature.move_index_row(str(p), "foo", "complete")
    feature.move_index_row(str(p), "baz", "complete")

    text = p.read_text()
    assert text.count("## Completed Features") == 1
    assert "[baz](baz/)" in text and "[foo](foo/)" in text


def test_no_section_and_no_quick_reference_appends_at_end(tmp_path):
    p = _write(tmp_path, "# Feature Index\n")

    assert feature.move_index_row(str(p), "foo", "complete")

    assert p.read_text().endswith("## Completed Features\n\n| Feature | Status | Beta Flag | Builds On |\n"
                                  "|---------|--------|-----------|-----------|\n| [foo](foo/) | complete |  | - |\n\n")
