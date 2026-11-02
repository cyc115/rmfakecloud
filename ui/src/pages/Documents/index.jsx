import DocumentTree from "./Tree";
import apiservice from "../../services/api.service"
import { useEffect, useRef, useState } from "react";
import { useParams, useHistory } from "react-router-dom";
import { Container, Row, Col } from "react-bootstrap";
import File from "./File";
import Folder from "./Folder";
import { findPseudoNode } from "./pseudoTree";
import Navbar from 'react-bootstrap/Navbar';
import { BsSearch, BsFolder2Open } from "react-icons/bs";
import Form from 'react-bootstrap/Form';
import Button from 'react-bootstrap/Button';
import InputGroup from 'react-bootstrap/InputGroup';
import { toast } from "react-toastify";
import { useAuthState } from "../../common/useAuthContext";
import useIsMobile from "../../hooks/useIsMobile";

import styles from "./Documents.module.scss";

export default function DocumentList() {
  const [selected, setSelected] = useState(null);
  const [term, setTerm] = useState("");
  const [showSearch, setShowSearch] = useState(false);
  const [counter, setCounter] = useState(0);
  const [entries, setEntries] = useState([])
  const [initialSelectionSet, setInitialSelectionSet] = useState(false);
  const [treeHeight, setTreeHeight] = useState(700);

  const isMobile = useIsMobile();
  // On phones the tree and the folder pane stack; the tree starts hidden so the
  // folder pane (breadcrumbs + file list) is what you see first.
  const [showTree, setShowTree] = useState(!isMobile);
  const treeVisible = !isMobile || showTree;

  const { itemId } = useParams();
  const history = useHistory();
  const { state: { user } } = useAuthState();

  const treeRef = useRef(null);
  const treeContainerRef = useRef(null);
  const pageRef = useRef(null);
  const lastSelectedId = useRef(null);

  useEffect(() => {
    lastSelectedId.current = selected?.id || null;
  }, [selected]);

  useEffect(() => {
    if (lastSelectedId.current && treeRef.current && typeof treeRef.current.get === 'function') {
      const node = treeRef.current.get(lastSelectedId.current);
      if (node && node !== selected) {
        setSelected(node);
      }
    }
  }, [entries]);

  const toggleNode = (node) => {
    if (node == null) {
      return
    }
    if (typeof node.toggle !== 'function') {
      return
    }

    node.toggle()
  }

  // select from tree. node must extend NodeApi from react-arborist
  const onSelect = (node) => {
    setSelected(node);
    toggleNode(node);

    // Update URL with selected item ID
    if (node && node.id) {
      // Don't add root and trash to URL, keep as /documents
      if (node.id === 'root' || node.id === 'trash') {
        history.push('/documents');
      } else {
        history.push(`/documents/${node.id}`);
      }
    }
  };

  // Tree selection: on phones, opening a file hides the tree so the viewer
  // is not pushed below the fold.
  const onTreeSelect = (node) => {
    onSelect(node);
    if (isMobile && node && node.isLeaf) {
      setShowTree(false);
    }
  };

  // On phones the whole page scrolls; bring a new selection into view.
  useEffect(() => {
    if (isMobile && pageRef.current) {
      pageRef.current.scrollIntoView({ block: "start" });
    }
  }, [selected?.id, isMobile]);

  // While hidden the tree has zero height, so react-arborist's scroll-to-selection
  // leaves the list offset. Re-align once it is visible again.
  useEffect(() => {
    if (treeVisible && treeHeight > 0 && treeRef.current) {
      treeRef.current.scrollTo(selected?.id || "root");
    }
  }, [treeVisible, treeHeight]);

  const onUpdate = () => {
    setCounter(prev => prev+1);
  };

  useEffect(() => {
    // Only auto-select first item if there's no itemId in URL
    if (
      !initialSelectionSet &&
      !itemId &&
      selected === null &&
      treeRef.current &&
      treeRef.current.root &&
      treeRef.current.root.children[0]
    ) {
      setSelected(treeRef.current.root.children[0]);
      setInitialSelectionSet(true);
    }
  }, [entries, selected, initialSelectionSet, itemId]);

  useEffect(() => {
    const resizeObserver = new ResizeObserver((event) => {
      setTreeHeight(event[0].contentBoxSize[0].blockSize);
    });

    if (treeContainerRef.current) {
      resizeObserver.observe(treeContainerRef.current);
    }

    return () => {
      resizeObserver.disconnect();
    };
  }, []);

	useEffect(() => {
		const loadDocs = async () => {
			const { Trash, Entries } = await apiservice.listDocument()

			const root = {
				id: "root",
				name: "My Files",
				isFolder: true,
				icon: "device",
				children: Entries,
			}
			const trash = {
				id: "trash",
				name: "Trash",
				isFolder: true,
				icon: "trash",
				children: Trash,
			}
			setEntries([root, trash]);
		}

		loadDocs().catch(e => toast.error(e));
	},[counter])

  // Handle URL navigation: restore selection from URL parameter
  useEffect(() => {
    // Only proceed if we have entries and an itemId
    if (!entries.length || !itemId || initialSelectionSet) {
      return;
    }

    const pseudoNode = findPseudoNode(entries, itemId);

    if (!pseudoNode) {
      // Item doesn't exist in our data
      toast.warning(`Item not found, returning to root`);
      history.push('/documents');
      return;
    }

    // Set the selection directly
    setSelected(pseudoNode);
    setInitialSelectionSet(true);

    // Try to open parent folders in the tree if possible
    if (treeRef.current && typeof treeRef.current.openParents === 'function') {
      // Give tree a moment to render, then open parents
      setTimeout(() => {
        if (treeRef.current && typeof treeRef.current.openParents === 'function') {
          treeRef.current.openParents(itemId);
        }
      }, 100);
    }
  }, [entries, itemId, initialSelectionSet]);

  const toggleSearch = () => {
    setShowSearch(!showSearch);
    setTerm("");
    if (!showSearch) setShowTree(true);
  };

  return (
    <Container fluid ref={pageRef} className={styles.page}>
        <Row className={styles.row}>
          <Col md={4} className={styles.col}>
            <Navbar className={styles.toolbar}>
              <div className={`${styles.stretch} ${styles.userid}`}>{user.UserID}</div>
              <Button
                variant="outline"
                className="d-md-none"
                active={showTree}
                aria-pressed={showTree}
                aria-label={showTree ? "Hide folder tree" : "Show folder tree"}
                onClick={() => setShowTree(!showTree)}
              >
                <BsFolder2Open />
              </Button>
              <Button variant="outline" aria-label="Search" onClick={toggleSearch}><BsSearch/></Button>
            </Navbar>

            {showSearch && <div className={styles.toolbar}>
              <InputGroup className="mb-3">
                <InputGroup.Text>
                  <BsSearch />
                </InputGroup.Text>

                <Form.Control autoFocus size="sm" type="text" value={term} onChange={(e) => { setTerm(e.currentTarget.value); }} />
              </InputGroup>
            </div>}

            <div ref={treeContainerRef} className={`${styles.treeContainer} ${treeVisible ? "" : "d-none"}`}>
              <DocumentTree selection={selected} onSelect={onTreeSelect} treeRef={treeRef} term={term} entries={entries} height={treeHeight} />
            </div>
          </Col>
          <Col md={8} className={styles.col}>
            <div className={styles.pane}>
              {selected && selected.isLeaf && <File key={selected.id} file={selected} onSelect={onSelect} />}
              {selected && !selected.isLeaf && <Folder selection={selected} onSelect={onSelect} onUpdate={onUpdate} counter={counter} />}
            </div>
          </Col>
        </Row>
    </Container>
  );
}
