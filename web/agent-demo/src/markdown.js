import DOMPurify from "dompurify";
import { marked } from "marked";

marked.setOptions({
  breaks: true,
  gfm: true
});

export function renderMarkdown(content) {
  const value = String(content || "");
  return DOMPurify.sanitize(marked.parse(value));
}
