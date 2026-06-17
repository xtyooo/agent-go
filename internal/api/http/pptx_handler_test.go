package http

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/learn-demo/agent-go/internal/agents/pptx"
)

func TestPPTXLatestReturnsPreviewAndDownloadURLs(t *testing.T) {
	store := pptx.NewMemoryStore()
	inst, err := store.Create(context.Background(), "conv-1", "生成 AI Agent PPT")
	if err != nil {
		t.Fatalf("create ppt: %v", err)
	}
	if _, err := store.Update(context.Background(), inst.ID, func(current *pptx.Instance) {
		current.Status = pptx.StatusSuccess
		current.TemplateCode = "business-simple"
		current.FileURL = "mock://ppt/conv-1/1.pptx"
		current.PPTSchema = `{"slides":[{"pageType":"COVER","pageDesc":"封面页","data":{"title":{"type":"text","content":"AI Agent"},"subtitle":{"type":"text","content":"技术分享"}}}]}`
	}); err != nil {
		t.Fatalf("update ppt: %v", err)
	}

	handler := NewPPTXHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), store)
	req := httptest.NewRequest(http.MethodGet, "/pptx/latest?conversationId=conv-1", nil)
	res := httptest.NewRecorder()

	handler.Latest(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{`"success":true`, `"conversationId":"conv-1"`, `"pageCount":1`, `"previewUrl":"/pptx/1/preview"`, `"downloadUrl":"/pptx/1/download"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s: %s", want, body)
		}
	}
}

func TestPPTXDownloadReturnsPPTXPackage(t *testing.T) {
	store := pptx.NewMemoryStore()
	inst, err := store.Create(context.Background(), "conv-1", "生成 AI Agent PPT")
	if err != nil {
		t.Fatalf("create ppt: %v", err)
	}
	if _, err := store.Update(context.Background(), inst.ID, func(current *pptx.Instance) {
		current.Status = pptx.StatusSuccess
		current.PPTSchema = `{"slides":[{"pageType":"COVER","pageDesc":"封面页","data":{"title":{"type":"text","content":"AI Agent"},"bullets":{"type":"text","content":"规划\n执行\n复盘"}}}]}`
	}); err != nil {
		t.Fatalf("update ppt: %v", err)
	}

	handler := NewPPTXHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), store)
	req := httptest.NewRequest(http.MethodGet, "/pptx/1/download", nil)
	req = req.WithContext(context.Background())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("pptId", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	res := httptest.NewRecorder()

	handler.Download(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "application/vnd.openxmlformats-officedocument.presentationml.presentation" {
		t.Fatalf("Content-Type = %q", got)
	}
	reader, err := zip.NewReader(bytes.NewReader(res.Body.Bytes()), int64(res.Body.Len()))
	if err != nil {
		t.Fatalf("download is not zip pptx: %v", err)
	}
	var hasPresentation, hasSlide bool
	for _, file := range reader.File {
		if file.Name == "ppt/presentation.xml" {
			hasPresentation = true
		}
		if file.Name == "ppt/slides/slide1.xml" {
			hasSlide = true
		}
	}
	if !hasPresentation || !hasSlide {
		t.Fatalf("pptx missing required files: presentation=%v slide=%v", hasPresentation, hasSlide)
	}
}
