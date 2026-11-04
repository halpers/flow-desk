package logic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
)

const MaxKnowledgeRequestBytes int64 = 4 * 1024 * 1024
const maxPDFBytes = 2 * 1024 * 1024
const maxPDFPages = 50
const maxPDFTextBytes = 512 * 1024
const maxPDFTextCharacters = 100000
const pdfParserVersion = "pypdf-6.21.4-text-v1"

type knowledgeFile struct {
	format, content string
	raw             []byte
}

func knowledgeUpload(filename, content, encoded string) (knowledgeFile, error) {
	if !strings.HasSuffix(strings.ToLower(filename), ".pdf") {
		if encoded != "" || !validateUpload(filename, content) {
			return knowledgeFile{}, errArgument
		}
		return knowledgeFile{format: "text", content: content, raw: []byte{}}, nil
	}
	if len(filename) > 180 || strings.ContainsAny(filename, "/\\\x00") || !utf8.ValidString(filename) || content != "" || encoded == "" {
		return knowledgeFile{}, errArgument
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(maxPDFBytes) {
		return knowledgeFile{}, failure(413, "PDF_TOO_LARGE", "PDF 不能超过 2 MiB")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || !bytes.HasPrefix(raw, []byte("%PDF-")) {
		return knowledgeFile{}, failure(400, "PDF_INVALID", "PDF 文件格式不正确")
	}
	if len(raw) > maxPDFBytes {
		return knowledgeFile{}, failure(413, "PDF_TOO_LARGE", "PDF 不能超过 2 MiB")
	}
	return knowledgeFile{format: "pdf", raw: raw}, nil
}

func pageTexts(raw string) []types.KnowledgePageText {
	pages := []types.KnowledgePageText{}
	_ = json.Unmarshal([]byte(raw), &pages)
	return pages
}

func validPDFPages(pages []types.KnowledgePageText) bool {
	if len(pages) == 0 || len(pages) > maxPDFPages {
		return false
	}
	characters, total, nonempty := 0, 0, false
	for position, page := range pages {
		if page.PageNumber != int64(position+1) || !utf8.ValidString(page.Text) || strings.ContainsRune(page.Text, 0) {
			return false
		}
		characters += utf8.RuneCountInString(page.Text)
		total += len(page.Text)
		nonempty = nonempty || strings.TrimSpace(page.Text) != ""
	}
	return nonempty && characters <= maxPDFTextCharacters && total <= maxPDFTextBytes
}

func applyIndexResult(row *model.DocumentVersions, req *types.IndexCompleteReq) error {
	if row.Id != req.VersionId || row.ContentHash != req.ContentHash {
		return errArgument
	}
	if req.Status == "failed" {
		allowed := []string{"PDF_INVALID", "PDF_ENCRYPTED", "PDF_OCR_REQUIRED", "PDF_LIMIT_EXCEEDED", "PDF_PARSE_TIMEOUT", "INDEX_FAILED"}
		if !slices.Contains(allowed, req.ErrorCode) || req.ChunkCount != 0 || len(req.PageTexts) != 0 || req.ParserVersion != "" {
			return errArgument
		}
	} else if req.ErrorCode != "" {
		return errArgument
	}
	content, pages, parser := row.Content, "[]", ""
	if req.Status == "ready" && row.SourceFormat == "pdf" {
		if req.ParserVersion != pdfParserVersion || !validPDFPages(req.PageTexts) {
			return errArgument
		}
		texts := make([]string, 0, len(req.PageTexts))
		for _, page := range req.PageTexts {
			texts = append(texts, page.Text)
		}
		content = strings.Join(texts, "\n")
		// The per-page limit also bounds the normalized document including separators.
		if utf8.RuneCountInString(content) > maxPDFTextCharacters || len(content) > maxPDFTextBytes {
			return errArgument
		}
		pages, parser = marshal(req.PageTexts), req.ParserVersion
	} else if len(req.PageTexts) != 0 || req.ParserVersion != "" {
		return errArgument
	}
	if row.IndexStatus != "pending" {
		if row.IndexStatus != req.Status || row.ChunkCount != req.ChunkCount || row.ErrorCode != req.ErrorCode || row.ParserVersion != parser || marshal(pageTexts(row.PageTexts)) != pages || row.Content != content {
			return failure(409, "INDEX_RESULT_CONFLICT", "索引结果已经确定，不能覆盖")
		}
		return nil
	}
	row.Content, row.PageTexts, row.ParserVersion = content, pages, parser
	row.IndexStatus, row.ChunkCount, row.ErrorCode = req.Status, req.ChunkCount, req.ErrorCode
	return nil
}

func excerptOnPage(version model.ActiveKnowledgeVersion, candidate types.EvidenceCandidate) bool {
	if version.SourceFormat != "pdf" {
		return candidate.PageNumber == 0 && strings.Contains(version.Content, candidate.Excerpt)
	}
	for _, page := range pageTexts(version.PageTexts) {
		if page.PageNumber == candidate.PageNumber {
			return strings.Contains(page.Text, candidate.Excerpt)
		}
	}
	return false
}

func PreviewVersionPages(ctx context.Context, s *svc.ServiceContext, req *types.VersionPagesReq) (types.VersionPagesData, error) {
	result := types.VersionPagesData{Pages: []types.KnowledgePageText{}}
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return result, err
	}
	if !validID(req.DocumentId) || !validID(req.VersionId) {
		return result, errArgument
	}
	row, err := s.DocumentVersionsModel.FindOneByTenantIdId(ctx, actor.TenantId, req.VersionId)
	if err != nil {
		return result, err
	}
	if row.DocumentId != req.DocumentId {
		return result, errMissing
	}
	if row.IndexStatus != "ready" {
		return result, failure(409, "INDEX_NOT_READY", "提取文本尚未就绪")
	}
	result.VersionId, result.ParserVersion, result.Pages = row.Id, row.ParserVersion, pageTexts(row.PageTexts)
	return result, nil
}
