// Tags shown in the viewer, from GET documents/:id/tags
// ({tags: [...], pages: [{id, tags: [...]}] | null}).

// pageTagsAt the tags of viewer page n (1-based). null when the page list
// does not match the rendered PDF (unreadable list, or a fallback export with
// another page count): a tag shown on the wrong page is worse than none.
export function pageTagsAt(docTags, n, numPages) {
  const pages = docTags && docTags.pages;
  if (!Array.isArray(pages) || pages.length !== numPages) return null;
  const p = pages[n - 1];
  return p ? p.tags : null;
}

// taggedPages the 1-based page numbers that carry a tag, with their tags;
// empty when the page list does not match the PDF
export function taggedPages(docTags, numPages) {
  const pages = docTags && docTags.pages;
  if (!Array.isArray(pages) || pages.length !== numPages) return [];
  const out = [];
  pages.forEach((p, i) => {
    if (p.tags.length) out.push({ page: i + 1, tags: p.tags });
  });
  return out;
}
