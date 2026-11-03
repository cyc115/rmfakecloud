import { useState, useEffect, useRef, useMemo } from "react";
import { useHistory, useLocation } from "react-router-dom";
import { parsePageParam, clampPage, withPageParam, parsePageInput } from "./pageParam";
import { pageTagsAt, taggedPages } from "./docTags";
import { Button, ButtonGroup, Dropdown } from "react-bootstrap";
import { toast } from "react-toastify";
import TagRow from "./TagRow";
import Navbar from 'react-bootstrap/Navbar';
import { FaChevronRight, FaChevronLeft, } from "react-icons/fa6";
import { AiOutlineDownload } from "react-icons/ai";
import constants from "../../common/constants";
import useIsMobile from "../../hooks/useIsMobile";

import apiservice from "../../services/api.service"
import NameTag from "../../components/NameTag"

import { pdfjs, Document, Page } from "react-pdf";
// import 'react-pdf/dist/Page/AnnotationLayer.css';
// import 'react-pdf/dist/Page/TextLayer.css';
// import 'react-pdf/dist/Page/TextLayer.css';
//pdfjs.GlobalWorkerOptions.workerSrc = "/pdf.worker.js"
// pdfjs.GlobalWorkerOptions.workerSrc = new URL(
//   'pdfjs-dist/build/pdf.worker.min.mjs',
//   import.meta.url,
// ).toString(); 


export default function FileViewer({ file, onSelect }) {
  const { data } = file;

  const downloadUrl = `${constants.ROOT_URL}/documents/${file.id}`;

  const history = useHistory();
  const location = useLocation();
  // the URL's ?page=N; read through a ref so onLoadSuccess sees the current one
  const wantedPage = useRef(null);
  wantedPage.current = parsePageParam(location.search);

  const [page, setPage] = useState(() => wantedPage.current || 1);
  const [pages, setPages] = useState(1);
  // Page renders only once numPages is known, so a ?page=99 is clamped first
  const [loaded, setLoaded] = useState(false);
  const [width, setWidth] = useState(0);
  const [height, setHeight] = useState(100);
  // Desktop fits the page to the pane's height; on phones the page scrolls,
  // so fit to the width instead.
  const isMobile = useIsMobile();
  // set the page and keep the address in step (replace: paging adds no history)
  const goTo = (n) => {
    setPage(n);
    history.replace({ search: withPageParam(history.location.search, n) });
  };
  const onLoadSuccess = (pdf) => {
    setPages(pdf.numPages);
    setLoaded(true);
    if (wantedPage.current === null) {
      setPage(1);
      return;
    }
    const n = clampPage(wantedPage.current, pdf.numPages);
    setPage(n);
    if (n !== wantedPage.current) {
      history.replace({ search: withPageParam(history.location.search, n) });
    }
  };
  // the page field: a draft while typing, committed on Enter or blur
  const [draft, setDraft] = useState(String(page));
  useEffect(() => { setDraft(String(page)); }, [page]);
  const commitDraft = () => {
    const n = parsePageInput(draft, pages);
    if (n === null || n === page) { setDraft(String(page)); return; }
    goTo(n);
  };
  const onDraftKey = (e) => {
    if (e.key === "Enter") { e.preventDefault(); commitDraft(); e.currentTarget.blur(); }
    else if (e.key === "Escape") { setDraft(String(page)); e.currentTarget.blur(); }
  };
  // document and page tags; null until loaded, or when they cannot be read
  const [docTags, setDocTags] = useState(null);
  useEffect(() => {
    let live = true;
    setDocTags(null);
    apiservice.documentTags(file.id)
      .then((t) => { if (live) setDocTags(t); })
      .catch(() => {});
    return () => { live = false; };
  }, [file.id]);
  // suggestions for the "+ tag" fields: every tag in use (page tags, document tags)
  const [suggest, setSuggest] = useState([]);
  useEffect(() => {
    apiservice.getPageTags().then((r) => setSuggest((r.tags || []).map((t) => t.name))).catch(() => {});
  }, []);
  const [busy, setBusy] = useState(false);
  const editTags = (change) => {
    setBusy(true);
    apiservice.editDocumentTags(file.id, change)
      .then((t) => setDocTags(t))
      .catch((e) => toast.error(`Could not change tags: ${e.message || e}`))
      .finally(() => setBusy(false));
  };
  const pageTags = loaded ? pageTagsAt(docTags, page, pages) : null;
  const pageId = pageTags && docTags.pages[page - 1].id;
  const tagged = loaded ? taggedPages(docTags, pages) : [];
  const docTagList = (docTags && docTags.tags) || [];

  const onPrev = () => goTo(Math.max(page - 1, 1));
  const onNext = () => goTo(Math.min(page + 1, pages));
  const parent = useRef(null);
  useEffect(() => {
    const resizeObserver = new ResizeObserver((event) => {
      setWidth(event[0].contentBoxSize[0].inlineSize);
      setHeight(event[0].contentBoxSize[0].blockSize);
    });

    resizeObserver.observe(parent.current);
    return () => resizeObserver.disconnect();
  }, []);

  // TODO: add loading and error handling
  const triggerDownload = (blob, filename) => {
    var url = window.URL.createObjectURL(blob)
    var a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    a.remove()
  }

  const onDownloadPdf = () => {
    apiservice.download(data.id)
      .then(blob => triggerDownload(blob, data.name + '.pdf'))
      .catch(() => {})
  }

  const onDownloadRmdoc = () => {
    apiservice.download(data.id, 'rmdoc')
      .then(blob => triggerDownload(blob, data.name + '.rmdoc'))
      .catch(() => {})
  }

  let options = useMemo(()=> {
    return {
      worker: new pdfjs.PDFWorker()
    }
  },[pdfjs])

  return (
    <>
      <Navbar style={{ marginLeft: '-12px' }}>
        { file && (<div className="breadcrumbs"><NameTag node={file} onSelect={onSelect} /></div>) }
      </Navbar>

      <Navbar>
        {pages > 1 && (
          <div>
            <ButtonGroup aria-label="Basic example">
              <Button size="sm" variant="outline-secondary" aria-label="Previous page" onClick={onPrev}><FaChevronLeft /></Button>
              <Button size="sm" variant="outline-secondary" aria-label="Next page" onClick={onNext}><FaChevronRight /></Button>
            </ButtonGroup>
            <span style={{ margin: '0 10px', display: 'inline-flex', alignItems: 'center', gap: '6px' }}>
              Page:
              <input type="number" inputMode="numeric" min={1} max={pages} aria-label="Page number"
                value={draft} onChange={(e) => setDraft(e.target.value)} onKeyDown={onDraftKey} onBlur={commitDraft}
                onFocus={(e) => e.target.select()}
                style={{ width: `${String(pages).length + 3}ch`, textAlign: 'center', background: 'transparent',
                  color: 'inherit', border: '1px solid var(--bs-border-color, #555)', borderRadius: '4px', padding: '1px 2px' }} />
              of {pages}
            </span>
          </div>
        )}
        <div style={{ flex: 1 }}></div>

        <Dropdown align="end">
          <Dropdown.Toggle size="sm" variant="secondary">
            <AiOutlineDownload />
          </Dropdown.Toggle>
          <Dropdown.Menu>
            <Dropdown.Item onClick={onDownloadPdf}>Download PDF</Dropdown.Item>
            <Dropdown.Item onClick={onDownloadRmdoc}>Download .rmdoc</Dropdown.Item>
          </Dropdown.Menu>
        </Dropdown>

      </Navbar>

      {docTags && (
        <div className="doc-tags" style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: '12px', marginBottom: '8px' }}>
          <datalist id="doc-tag-suggestions">{suggest.map((t) => <option key={t} value={t} />)}</datalist>
          <TagRow label="Document" tags={docTagList} busy={busy} listId="doc-tag-suggestions"
            onAdd={(t) => editTags({ add: [t] })} onRemove={(t) => editTags({ remove: [t] })} />
          {pageTags && <TagRow label={`Page ${page}`} tags={pageTags} busy={busy} listId="doc-tag-suggestions" bg="info" text="dark"
            onAdd={(t) => editTags({ pageId, add: [t] })} onRemove={(t) => editTags({ pageId, remove: [t] })} />}
          {tagged.length > 0 && (
            <Dropdown style={{ marginLeft: 'auto' }}>
              <Dropdown.Toggle size="sm" variant="outline-secondary">
                Tagged pages ({tagged.length})
              </Dropdown.Toggle>
              <Dropdown.Menu>
                {tagged.map((p) => (
                  <Dropdown.Item key={p.page} active={p.page === page} onClick={() => goTo(p.page)}>
                    Page {p.page}: {p.tags.join(", ")}
                  </Dropdown.Item>
                ))}
              </Dropdown.Menu>
            </Dropdown>
          )}
        </div>
      )}

      {file && (
        <div ref={parent} style={{height: isMobile ? "auto" : "95%"}}>
		  <Document file={downloadUrl} onLoadSuccess={onLoadSuccess} options={options}>
            {loaded && <Page pageNumber={page}
              {...(isMobile ? { width: width || undefined } : { height })}
              renderAnnotationLayer={false}
              renderTextLayer={false}
            />}
          </Document>
        </div>
      )}
    </>
  );
}
