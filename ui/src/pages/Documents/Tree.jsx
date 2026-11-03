import { Tree } from 'react-arborist';
import { useMemo, useState } from 'react';
import FileIcon from './FileIcon';
import { MOVE_TYPE, canDrop, movedIds } from './move';

import styles from "./Documents.module.scss"

const DocumentTree = ({ selection, onSelect, treeRef, term, entries, height = 700, onMove, findNode }) => {
  // react-arborist's react-dnd backend listens on window by default and cancels every native drag
  // it did not start (the file list's moves). The tree's own drag and drop is off, so it gets a
  // detached element that never sees an event.
  const dndRoot = useMemo(() => document.createElement("div"), []);
  const onTreeSelect = (sel) => {
    if (sel.length > 0) {
      const node = sel[0];
      onSelect(node);
    }
  }

  function Node({ node, style, dragHandle }) {
    const [over, setOver] = useState(false);
    // getData is empty during dragover in browsers: accept any move drag onto a folder, check on drop
    const onDragOver = (e) => {
      if (!onMove || !node.data.isFolder || !Array.from(e.dataTransfer.types || []).includes(MOVE_TYPE)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      setOver(true);
    };
    const onDrop = (e) => {
      setOver(false);
      const ids = movedIds(e);
      if (!ids) return;
      e.preventDefault();
      if (canDrop(node, ids, findNode)) onMove(ids, node);
    };
    return (
      <div
        style={style}
        ref={dragHandle}
        className={`${node.isSelected ? styles.selected : ""} ${over ? "drop-target" : ""}`}
        onDragOver={onDragOver}
        onDragLeave={() => setOver(false)}
        onDrop={onDrop}
      >
        <div className={itemClassName(node.data)}>
          <FileIcon file={node.data} />
          {node.data.name}
        </div>
      </div>
    );
  }

  function Cursor({ top, left }) {
    return <div style={{ top, left }}></div>;
  }

  const itemClassName = (item) => {
    if (item.isFolder) return "treeitem-nodename is-folder";
    return "treeitem-nodename";
  }

  if (entries && !entries.length) {
    return <div>No documents</div>;
  }
  return (
    <div>
      <Tree
        ref={(arg) => {
          if (treeRef.current == null) {
            if (arg) treeRef.current = arg
          }

          return treeRef.current
        }}
        data={entries}
        selection={selection?.id}
        rowHeight={36}
        indent={36}
        width="100%"
        height={height}
        renderCursor={Cursor}
        searchTerm={term}
        onSelect={onTreeSelect}
        className="documents-tree"
        disableEdit={true}
        disableDrag={true}
        disableDrop={true}
        dndRootElement={dndRoot}
        openByDefault={false}
      >
        {Node}
      </Tree>
    </div>
  )
}
export default DocumentTree;
