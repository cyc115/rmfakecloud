#!/usr/bin/env python3
"""Generate the synthetic .rm (v6) page fixtures used by the fake tablet tests
and the e2e harness. No real handwriting: strokes are generated geometry.

    uv run --with rmscene==0.8.0 python scripts/e2e/make_fixtures.py OUTDIR

Writes strokes_a.rm (3 strokes), strokes_ab.rm (the same 3 strokes with the
same CRDT item ids, plus 2 new ones), text_todo.rm (typed to-do list) and
text_note.rm (typed text, no tasks: a legible page for transcribe_only).
"""
import math
import sys
from pathlib import Path
from uuid import UUID

import rmscene
from rmscene import scene_items as si
from rmscene import (
    AuthorIdsBlock, CrdtId, CrdtSequenceItem, LwwValue, MigrationInfoBlock,
    PageInfoBlock, SceneGroupItemBlock, SceneLineItemBlock, SceneTreeBlock,
    TreeNodeBlock, write_blocks,
)

AUTHOR = UUID("00000000-0000-4000-8000-000000000001")
TEXT = "TODO\n- [ ] Buy oat milk\n- [ ] Call the vet -inbox"
NOTE = "Notes\nThe parcel arrived on Monday."
LAYER = CrdtId(0, 11)


def stroke(kind: int) -> si.Line:
    """A synthetic stroke: kind 0 = wave, 1 = diagonal, 2 = circle, 3 = zigzag, 4 = arc."""
    pts = []
    for i in range(24):
        u = i / 23
        if kind == 0:
            x, y = -300 + 600 * u, -400 + 40 * math.sin(u * 6.28)
        elif kind == 1:
            x, y = -300 + 600 * u, -200 + 300 * u
        elif kind == 2:
            x, y = 100 * math.cos(u * 6.28), 100 * math.sin(u * 6.28)
        elif kind == 3:
            x, y = -250 + 500 * u, 300 + (40 if i % 2 else -40)
        else:
            x, y = 200 * math.cos(u * 3.14), 450 + 80 * math.sin(u * 3.14)
        pts.append(si.Point(x=x, y=y, speed=30, direction=0, width=14, pressure=100))
    return si.Line(
        color=si.PenColor.BLACK, tool=si.Pen.BALLPOINT_1, points=pts,
        thickness_scale=2.0, starting_length=0.0,
    )


def header_blocks(text_chars=1, text_lines=1):
    yield AuthorIdsBlock(author_uuids={1: AUTHOR})
    yield MigrationInfoBlock(migration_id=CrdtId(1, 1), is_device=True)
    yield PageInfoBlock(loads_count=1, merges_count=0,
                        text_chars_count=text_chars, text_lines_count=text_lines)
    yield SceneTreeBlock(tree_id=LAYER, node_id=CrdtId(0, 0), is_update=True,
                         parent_id=CrdtId(0, 1))
    yield TreeNodeBlock(si.Group(node_id=CrdtId(0, 1)))
    yield TreeNodeBlock(si.Group(node_id=LAYER, label=LwwValue(timestamp=CrdtId(0, 12), value="Layer 1")))
    yield SceneGroupItemBlock(
        parent_id=CrdtId(0, 1),
        item=CrdtSequenceItem(item_id=CrdtId(0, 13), left_id=CrdtId(0, 0),
                              right_id=CrdtId(0, 0), deleted_length=0, value=LAYER))


def strokes_blocks(n: int):
    yield from header_blocks()
    left = CrdtId(0, 0)
    for k in range(n):
        item_id = CrdtId(1, 20 + k)  # same ids for the same stroke index in every file
        yield SceneLineItemBlock(
            parent_id=LAYER,
            item=CrdtSequenceItem(item_id=item_id, left_id=left, right_id=CrdtId(0, 0),
                                  deleted_length=0, value=stroke(k)))
        left = item_id


def write(path: Path, blocks) -> None:
    with open(path, "wb") as f:
        write_blocks(f, blocks)
    with open(path, "rb") as f:
        head = f.read(43)
    assert head.startswith(b"reMarkable .lines file, version=6"), head


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    out = Path(sys.argv[1])
    out.mkdir(parents=True, exist_ok=True)
    write(out / "strokes_a.rm", strokes_blocks(3))
    write(out / "strokes_ab.rm", strokes_blocks(5))
    write(out / "text_todo.rm", rmscene.simple_text_document(TEXT, AUTHOR))
    write(out / "text_note.rm", rmscene.simple_text_document(NOTE, AUTHOR))


if __name__ == "__main__":
    main()
