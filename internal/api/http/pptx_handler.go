package http

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/learn-demo/agent-go/internal/agents/pptx"
)

type PPTXHandler struct {
	logger *slog.Logger
	store  pptx.Store
}

type pptxInstanceResponse struct {
	Success        bool            `json:"success"`
	ID             int64           `json:"id"`
	ConversationID string          `json:"conversationId"`
	TemplateCode   string          `json:"templateCode"`
	Status         string          `json:"status"`
	Query          string          `json:"query"`
	Requirement    string          `json:"requirement"`
	Outline        string          `json:"outline"`
	SearchInfo     string          `json:"searchInfo"`
	PPTSchema      string          `json:"pptSchema"`
	FileURL        string          `json:"fileUrl"`
	ErrorMsg       string          `json:"errorMsg"`
	PageCount      int             `json:"pageCount"`
	PreviewURL     string          `json:"previewUrl"`
	DownloadURL    string          `json:"downloadUrl"`
	RendererStatus string          `json:"rendererStatus"`
	Downloadable   bool            `json:"downloadable"`
	CreateTime     string          `json:"createTime"`
	UpdateTime     string          `json:"updateTime"`
	Slides         []pptxSlideView `json:"slides"`
}

type pptxSchemaDocument struct {
	Slides []pptxSchemaSlide `json:"slides"`
	Pages  []pptxSchemaSlide `json:"pages"`
}

type pptxSchemaSlide struct {
	PageType          string                    `json:"pageType"`
	PageDesc          string                    `json:"pageDesc"`
	TemplatePageIndex int                       `json:"templatePageIndex"`
	Data              map[string]pptxFieldValue `json:"data"`
	Fields            map[string]pptxFieldValue `json:"fields"`
}

type pptxFieldValue struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	URL       string `json:"url"`
	FontLimit int    `json:"fontLimit"`
}

type pptxSlideView struct {
	PageType string   `json:"pageType"`
	PageDesc string   `json:"pageDesc"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle"`
	Body     []string `json:"body"`
}

func NewPPTXHandler(logger *slog.Logger, store pptx.Store) *PPTXHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &PPTXHandler{logger: logger, store: store}
}

func (h *PPTXHandler) Latest(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "pptx store is not available",
		})
		return
	}
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
	if conversationID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "conversationId is required",
		})
		return
	}

	inst, ok, err := h.store.Latest(r.Context(), conversationID)
	h.writeInstance(w, r, inst, ok, err)
}

func (h *PPTXHandler) Detail(w http.ResponseWriter, r *http.Request) {
	inst, ok, err := h.loadInstance(r)
	h.writeInstance(w, r, inst, ok, err)
}

func (h *PPTXHandler) Preview(w http.ResponseWriter, r *http.Request) {
	inst, ok, err := h.loadInstance(r)
	if err != nil {
		h.logger.Warn("PPT preview load failed", "error", err)
		http.Error(w, "ppt instance load failed", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	slides := slidesFromSchema(inst.PPTSchema)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	if _, err := io.WriteString(w, renderPPTPreviewHTML(inst, slides)); err != nil {
		h.logger.Warn("PPT preview write failed", "ppt_id", inst.ID, "error", err)
	}
}

func (h *PPTXHandler) Download(w http.ResponseWriter, r *http.Request) {
	inst, ok, err := h.loadInstance(r)
	if err != nil {
		h.logger.Warn("PPT download load failed", "error", err)
		http.Error(w, "ppt instance load failed", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	slides := slidesFromSchema(inst.PPTSchema)
	data, err := buildMinimalPPTX(slides, inst)
	if err != nil {
		h.logger.Warn("PPT download build failed", "ppt_id", inst.ID, "error", err)
		http.Error(w, "pptx build failed", http.StatusInternalServerError)
		return
	}

	filename := fmt.Sprintf("kimo-agent-ppt-%d.pptx", inst.ID)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.presentationml.presentation")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if _, err := w.Write(data); err != nil {
		h.logger.Warn("PPT download write failed", "ppt_id", inst.ID, "error", err)
	}
}

func (h *PPTXHandler) writeInstance(w http.ResponseWriter, r *http.Request, inst pptx.Instance, ok bool, err error) {
	if err != nil {
		h.logger.Warn("PPT instance query failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "query ppt instance failed",
		})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "ppt instance not found",
		})
		return
	}
	writeJSON(w, http.StatusOK, toPPTXInstanceResponse(r, inst))
}

func (h *PPTXHandler) loadInstance(r *http.Request) (pptx.Instance, bool, error) {
	if h.store == nil {
		return pptx.Instance{}, false, nil
	}
	id, err := pptIDFromRequest(r)
	if err != nil {
		return pptx.Instance{}, false, err
	}
	if id > 0 {
		return h.store.Get(r.Context(), id)
	}
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
	return h.store.Latest(r.Context(), conversationID)
}

func pptIDFromRequest(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(chi.URLParam(r, "pptId"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid ppt id")
	}
	return id, nil
}

func toPPTXInstanceResponse(r *http.Request, inst pptx.Instance) pptxInstanceResponse {
	slides := slidesFromSchema(inst.PPTSchema)
	previewURL := fmt.Sprintf("/pptx/%d/preview", inst.ID)
	downloadURL := fmt.Sprintf("/pptx/%d/download", inst.ID)
	return pptxInstanceResponse{
		Success:        true,
		ID:             inst.ID,
		ConversationID: inst.ConversationID,
		TemplateCode:   inst.TemplateCode,
		Status:         string(inst.Status),
		Query:          inst.Query,
		Requirement:    inst.Requirement,
		Outline:        inst.Outline,
		SearchInfo:     inst.SearchInfo,
		PPTSchema:      inst.PPTSchema,
		FileURL:        inst.FileURL,
		ErrorMsg:       inst.ErrorMsg,
		PageCount:      len(slides),
		PreviewURL:     previewURL,
		DownloadURL:    downloadURL,
		RendererStatus: rendererStatus(inst.FileURL),
		Downloadable:   strings.TrimSpace(inst.PPTSchema) != "",
		CreateTime:     formatAPITime(inst.CreateTime),
		UpdateTime:     formatAPITime(inst.UpdateTime),
		Slides:         slides,
	}
}

func rendererStatus(fileURL string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(fileURL)), "mock://") {
		return "basic-pptx"
	}
	if strings.TrimSpace(fileURL) == "" {
		return "pending"
	}
	return "external"
}

func slidesFromSchema(schema string) []pptxSlideView {
	var doc pptxSchemaDocument
	if err := json.Unmarshal([]byte(strings.TrimSpace(schema)), &doc); err != nil {
		return nil
	}
	source := doc.Slides
	if len(source) == 0 {
		source = doc.Pages
	}
	views := make([]pptxSlideView, 0, len(source))
	for index, slide := range source {
		view := slideToView(slide, index)
		views = append(views, view)
	}
	return views
}

func slideToView(slide pptxSchemaSlide, index int) pptxSlideView {
	fields := slide.Data
	if len(fields) == 0 {
		fields = slide.Fields
	}
	view := pptxSlideView{
		PageType: strings.TrimSpace(slide.PageType),
		PageDesc: strings.TrimSpace(slide.PageDesc),
	}
	if view.PageDesc == "" {
		view.PageDesc = fmt.Sprintf("第 %d 页", index+1)
	}
	for name, field := range fields {
		text := strings.TrimSpace(field.Content)
		if text == "" && field.URL != "" {
			text = strings.TrimSpace(field.URL)
		}
		if text == "" {
			continue
		}
		lowerName := strings.ToLower(name)
		switch {
		case view.Title == "" && strings.Contains(lowerName, "title"):
			view.Title = text
		case view.Subtitle == "" && (strings.Contains(lowerName, "subtitle") || strings.Contains(lowerName, "summary")):
			view.Subtitle = text
		default:
			view.Body = append(view.Body, splitSlideText(text)...)
		}
	}
	if view.Title == "" {
		view.Title = view.PageDesc
	}
	return view
}

func splitSlideText(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "；", "\n")
	text = strings.ReplaceAll(text, ";", "\n")
	var items []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "-•· "))
		if line != "" {
			items = append(items, line)
		}
	}
	if len(items) == 0 && strings.TrimSpace(text) != "" {
		items = append(items, strings.TrimSpace(text))
	}
	return items
}

func renderPPTPreviewHTML(inst pptx.Instance, slides []pptxSlideView) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>KimoAgent PPT Preview</title><style>`)
	b.WriteString(`:root{color-scheme:dark;font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei",sans-serif;background:#08090b;color:#f5f5f6}*{box-sizing:border-box}body{margin:0;background:#08090b;color:#f5f5f6}.deck{display:grid;gap:22px;max-width:1120px;margin:0 auto;padding:28px 18px 42px}.top{display:flex;align-items:end;justify-content:space-between;gap:16px;border-bottom:1px solid rgba(255,255,255,.12);padding-bottom:18px}.top h1{margin:0;font-size:18px;font-weight:680}.top p{margin:6px 0 0;color:#a8aaad;font-size:13px}.slide{aspect-ratio:16/9;border:1px solid rgba(255,255,255,.14);border-radius:14px;padding:42px 48px;background:linear-gradient(135deg,#141518 0%,#0b0c0e 64%,#1b1d20 100%);box-shadow:0 18px 44px rgba(0,0,0,.26);display:flex;flex-direction:column;justify-content:center;position:relative;overflow:hidden}.slide:before{content:"";position:absolute;inset:0;border-top:3px solid rgba(242,189,93,.78);opacity:.82}.slide small{color:#f2bd5d;font-size:13px;letter-spacing:0;text-transform:uppercase}.slide h2{margin:14px 0 0;font-size:clamp(26px,5vw,54px);line-height:1.08;font-weight:760;letter-spacing:0;max-width:82%}.slide p{margin:16px 0 0;color:#d9dadb;font-size:20px;line-height:1.45;max-width:84%}.slide ul{margin:22px 0 0;padding:0;list-style:none;display:grid;gap:12px;max-width:86%}.slide li{position:relative;padding-left:20px;color:#e6e6e7;font-size:19px;line-height:1.48}.slide li:before{content:"";position:absolute;left:0;top:.72em;width:7px;height:7px;border-radius:50%;background:#f2bd5d}.empty{border:1px dashed rgba(255,255,255,.18);border-radius:14px;padding:30px;color:#a8aaad}@media(max-width:720px){.deck{padding:18px 12px 28px}.top{display:block}.slide{padding:24px 22px}.slide h2{max-width:100%;font-size:28px}.slide p,.slide li{font-size:15px;max-width:100%}.slide ul{max-width:100%}}`)
	b.WriteString(`</style></head><body><main class="deck">`)
	b.WriteString(`<header class="top"><div><h1>`)
	b.WriteString(html.EscapeString(previewTitle(inst, slides)))
	b.WriteString(`</h1><p>`)
	b.WriteString(html.EscapeString(fmt.Sprintf("PPTBuilder · %d 页 · %s", len(slides), inst.UpdateTime.Format("2006-01-02 15:04"))))
	b.WriteString(`</p></div></header>`)
	if len(slides) == 0 {
		b.WriteString(`<section class="empty">当前实例还没有可预览的 PPT Schema。</section>`)
	} else {
		for i, slide := range slides {
			b.WriteString(`<section class="slide"><small>`)
			b.WriteString(html.EscapeString(fmt.Sprintf("%02d / %s", i+1, fallback(slide.PageType, slide.PageDesc))))
			b.WriteString(`</small><h2>`)
			b.WriteString(html.EscapeString(slide.Title))
			b.WriteString(`</h2>`)
			if slide.Subtitle != "" {
				b.WriteString(`<p>`)
				b.WriteString(html.EscapeString(slide.Subtitle))
				b.WriteString(`</p>`)
			}
			if len(slide.Body) > 0 {
				b.WriteString(`<ul>`)
				for _, item := range slide.Body {
					b.WriteString(`<li>`)
					b.WriteString(html.EscapeString(item))
					b.WriteString(`</li>`)
				}
				b.WriteString(`</ul>`)
			}
			b.WriteString(`</section>`)
		}
	}
	b.WriteString(`</main></body></html>`)
	return b.String()
}

func previewTitle(inst pptx.Instance, slides []pptxSlideView) string {
	for _, slide := range slides {
		if strings.TrimSpace(slide.Title) != "" {
			return slide.Title
		}
	}
	if strings.TrimSpace(inst.Requirement) != "" {
		return firstLine(inst.Requirement)
	}
	if strings.TrimSpace(inst.Query) != "" {
		return firstLine(inst.Query)
	}
	return "PPT 预览"
}

func fallback(value, fallbackValue string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(fallbackValue)
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, sep := range []string{"\n", "。", "；"} {
		if idx := strings.Index(value, sep); idx > 0 {
			value = value[:idx]
		}
	}
	runes := []rune(value)
	if len(runes) > 48 {
		return string(runes[:48])
	}
	return value
}

func buildMinimalPPTX(slides []pptxSlideView, inst pptx.Instance) ([]byte, error) {
	if len(slides) == 0 {
		slides = []pptxSlideView{{
			PageType: "EMPTY",
			PageDesc: "PPT 预览",
			Title:    previewTitle(inst, nil),
			Subtitle: "当前实例还没有可预览的 PPT Schema",
		}}
	}

	buf := bytes.NewBuffer(nil)
	zw := zip.NewWriter(buf)
	files := pptxPackageFiles(slides, inst)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := io.WriteString(w, content); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func pptxPackageFiles(slides []pptxSlideView, inst pptx.Instance) map[string]string {
	files := map[string]string{
		"[Content_Types].xml":                          contentTypesXML(len(slides)),
		"_rels/.rels":                                  packageRelsXML(),
		"docProps/app.xml":                             appXML(len(slides)),
		"docProps/core.xml":                            coreXML(inst),
		"ppt/presentation.xml":                         presentationXML(len(slides)),
		"ppt/_rels/presentation.xml.rels":              presentationRelsXML(len(slides)),
		"ppt/slideMasters/slideMaster1.xml":            slideMasterXML(),
		"ppt/slideMasters/_rels/slideMaster1.xml.rels": slideMasterRelsXML(),
		"ppt/slideLayouts/slideLayout1.xml":            slideLayoutXML(),
		"ppt/slideLayouts/_rels/slideLayout1.xml.rels": slideLayoutRelsXML(),
		"ppt/theme/theme1.xml":                         themeXML(),
		"ppt/presProps.xml":                            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentationPr xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`,
		"ppt/viewProps.xml":                            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:viewPr xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`,
		"ppt/tableStyles.xml":                          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:tblStyleLst xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`,
	}
	for index, slide := range slides {
		n := index + 1
		files[fmt.Sprintf("ppt/slides/slide%d.xml", n)] = slideXML(slide, index)
		files[fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n)] = slideRelsXML()
	}
	return files
}

func contentTypesXML(slideCount int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/><Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/><Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/><Override PartName="/ppt/presProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"/><Override PartName="/ppt/viewProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"/><Override PartName="/ppt/tableStyles.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>`)
	for i := 1; i <= slideCount; i++ {
		b.WriteString(fmt.Sprintf(`<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i))
	}
	b.WriteString(`</Types>`)
	return b.String()
}

func packageRelsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/></Relationships>`
}

func appXML(slideCount int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>KimoAgent</Application><PresentationFormat>On-screen Show (16:9)</PresentationFormat><Slides>%d</Slides><Notes>0</Notes><HiddenSlides>0</HiddenSlides><MMClips>0</MMClips><ScaleCrop>false</ScaleCrop><HeadingPairs><vt:vector size="2" baseType="variant"><vt:variant><vt:lpstr>Slides</vt:lpstr></vt:variant><vt:variant><vt:i4>%d</vt:i4></vt:variant></vt:vector></HeadingPairs><TitlesOfParts><vt:vector size="%d" baseType="lpstr">%s</vt:vector></TitlesOfParts><Company>KimoAgent</Company><LinksUpToDate>false</LinksUpToDate><SharedDoc>false</SharedDoc><HyperlinksChanged>false</HyperlinksChanged><AppVersion>16.0000</AppVersion></Properties>`, slideCount, slideCount, slideCount, appSlideTitles(slideCount))
}

func appSlideTitles(slideCount int) string {
	var b strings.Builder
	for i := 1; i <= slideCount; i++ {
		b.WriteString(fmt.Sprintf(`<vt:lpstr>Slide %d</vt:lpstr>`, i))
	}
	return b.String()
}

func coreXML(inst pptx.Instance) string {
	now := time.Now().UTC().Format(time.RFC3339)
	title := xmlEscape(previewTitle(inst, slidesFromSchema(inst.PPTSchema)))
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>%s</dc:title><dc:creator>KimoAgent</dc:creator><cp:lastModifiedBy>KimoAgent</cp:lastModifiedBy><dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified></cp:coreProperties>`, title, now, now)
}

func presentationXML(slideCount int) string {
	var slides strings.Builder
	for i := 1; i <= slideCount; i++ {
		slides.WriteString(fmt.Sprintf(`<p:sldId id="%d" r:id="rId%d"/>`, 255+i, i))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId%d"/></p:sldMasterIdLst><p:sldIdLst>%s</p:sldIdLst><p:sldSz cx="12192000" cy="6858000" type="screen16x9"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`, slideCount+1, slides.String())
}

func presentationRelsXML(slideCount int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := 1; i <= slideCount; i++ {
		b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, i, i))
	}
	b.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`, slideCount+1))
	b.WriteString(`</Relationships>`)
	return b.String()
}

func slideMasterXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:bg><p:bgPr><a:solidFill><a:srgbClr val="0B0C0E"/></a:solidFill><a:effectLst/></p:bgPr></p:bg><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree></p:cSld><p:clrMap accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" bg1="lt1" bg2="lt2" folHlink="folHlink" hlink="hlink" tx1="dk1" tx2="dk2"/><p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst><p:txStyles><p:titleStyle/><p:bodyStyle/><p:otherStyle/></p:txStyles></p:sldMaster>`
}

func slideMasterRelsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/></Relationships>`
}

func slideLayoutXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank" preserve="1"><p:cSld name="Blank"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`
}

func slideLayoutRelsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/></Relationships>`
}

func slideRelsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/></Relationships>`
}

func slideXML(slide pptxSlideView, index int) string {
	title := fallback(slide.Title, slide.PageDesc)
	subtitle := slide.Subtitle
	body := slide.Body
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:bg><p:bgPr><a:solidFill><a:srgbClr val="0B0C0E"/></a:solidFill><a:effectLst/></p:bgPr></p:bg><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`)
	b.WriteString(shapeXML(2, "Kicker", fmt.Sprintf("%02d / %s", index+1, fallback(slide.PageType, slide.PageDesc)), 685800, 457200, 7772400, 365760, 1600, "F2BD5D", false))
	b.WriteString(shapeXML(3, "Title", title, 685800, 1005840, 9144000, 1371600, 3600, "F5F5F6", true))
	if subtitle != "" {
		b.WriteString(shapeXML(4, "Subtitle", subtitle, 685800, 2377440, 9144000, 731520, 2000, "D9DADB", false))
	}
	if len(body) > 0 {
		b.WriteString(bulletShapeXML(5, body, 914400, 3429000, 9144000, 2438400))
	}
	b.WriteString(`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`)
	return b.String()
}

func shapeXML(id int, name, text string, x, y, cx, cy, fontSize int, color string, bold bool) string {
	boldAttr := ""
	if bold {
		boldAttr = ` b="1"`
	}
	return fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr><p:txBody><a:bodyPr wrap="square"/><a:lstStyle/><a:p><a:r><a:rPr lang="zh-CN" sz="%d"%s><a:solidFill><a:srgbClr val="%s"/></a:solidFill><a:latin typeface="Microsoft YaHei"/><a:ea typeface="Microsoft YaHei"/></a:rPr><a:t>%s</a:t></a:r><a:endParaRPr lang="zh-CN" sz="%d"/></a:p></p:txBody></p:sp>`, id, xmlEscape(name), x, y, cx, cy, fontSize, boldAttr, color, xmlEscape(text), fontSize)
}

func bulletShapeXML(id int, items []string, x, y, cx, cy int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<p:sp><p:nvSpPr><p:cNvPr id="%d" name="Bullets"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr><p:txBody><a:bodyPr wrap="square"/><a:lstStyle/>`, id, x, y, cx, cy))
	for _, item := range items {
		b.WriteString(`<a:p><a:pPr marL="342900" indent="-228600"><a:buChar char="•"/></a:pPr><a:r><a:rPr lang="zh-CN" sz="1900"><a:solidFill><a:srgbClr val="E6E6E7"/></a:solidFill><a:latin typeface="Microsoft YaHei"/><a:ea typeface="Microsoft YaHei"/></a:rPr><a:t>`)
		b.WriteString(xmlEscape(item))
		b.WriteString(`</a:t></a:r><a:endParaRPr lang="zh-CN" sz="1900"/></a:p>`)
	}
	b.WriteString(`</p:txBody></p:sp>`)
	return b.String()
}

func themeXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="KimoAgent"><a:themeElements><a:clrScheme name="KimoAgent"><a:dk1><a:srgbClr val="08090B"/></a:dk1><a:lt1><a:srgbClr val="F5F5F6"/></a:lt1><a:dk2><a:srgbClr val="111214"/></a:dk2><a:lt2><a:srgbClr val="D9DADB"/></a:lt2><a:accent1><a:srgbClr val="F2BD5D"/></a:accent1><a:accent2><a:srgbClr val="9BBCFF"/></a:accent2><a:accent3><a:srgbClr val="72D997"/></a:accent3><a:accent4><a:srgbClr val="FF6B6B"/></a:accent4><a:accent5><a:srgbClr val="C7C7C8"/></a:accent5><a:accent6><a:srgbClr val="686A6D"/></a:accent6><a:hlink><a:srgbClr val="9BBCFF"/></a:hlink><a:folHlink><a:srgbClr val="C7C7C8"/></a:folHlink></a:clrScheme><a:fontScheme name="KimoAgent"><a:majorFont><a:latin typeface="Microsoft YaHei"/><a:ea typeface="Microsoft YaHei"/><a:cs typeface="Arial"/></a:majorFont><a:minorFont><a:latin typeface="Microsoft YaHei"/><a:ea typeface="Microsoft YaHei"/><a:cs typeface="Arial"/></a:minorFont></a:fontScheme><a:fmtScheme name="KimoAgent"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln w="6350" cap="flat" cmpd="sng" algn="ctr"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme></a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`
}

func xmlEscape(value string) string {
	var b strings.Builder
	if err := xmlEscapeWriter(&b, value); err != nil {
		return ""
	}
	return b.String()
}

func xmlEscapeWriter(w io.Writer, value string) error {
	return xml.EscapeText(w, []byte(value))
}
