package daemon

import (
	"encoding/json"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
)

func (s *Server) handleFind(req *Request) *Response {
	var fp FindParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &fp); err != nil {
			return errorReply(req.ID, InternalError, "find: bad params: "+err.Error())
		}
	}
	if fp.Query == "" {
		return errorReply(req.ID, InternalError, "find: query is required")
	}
	s.mu.RLock()
	archive := s.archiveDB
	live := s.liveDB
	s.mu.RUnlock()

	hits, err := search.Run(archive, live, search.Params{
		Query:       fp.Query,
		Project:     fp.Project,
		DirType:     fp.DirType,
		SourceType:  fp.SourceType,
		Feature:     fp.Feature,
		Latest:      fp.Latest,
		LiveOnly:    fp.LiveOnly,
		ArchiveOnly: fp.ArchiveOnly,
		Since:       fp.Since,
		Until:       fp.Until,
		Limit:       fp.Limit,
		IncludeFull: fp.IncludeFull,
	})
	if err != nil {
		return errorReply(req.ID, InternalError, "find: "+err.Error())
	}
	return okReply(req.ID, map[string]any{"hits": hits})
}

// handleEmbed embeds one query string with the daemon's shared embedder. It
// rejects nil/stub embedders so the caller falls back rather than scoring real
// stored vectors against a non-semantic query vector.
func (s *Server) handleEmbed(req *Request) *Response {
	var ep EmbedParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &ep); err != nil {
			return errorReply(req.ID, InternalError, "embed: bad params: "+err.Error())
		}
	}
	if ep.Text == "" {
		return errorReply(req.ID, InternalError, "embed: text is required")
	}
	s.embedderMu.RLock()
	emb := s.embedder
	s.embedderMu.RUnlock()
	if emb == nil || strings.HasPrefix(emb.ModelName(), "stub:") {
		return errorReply(req.ID, InternalError, "embed: no real embedder available")
	}
	vec, err := emb.Embed(ep.Text)
	if err != nil {
		return errorReply(req.ID, InternalError, "embed: "+err.Error())
	}
	return okReply(req.ID, EmbedResult{Vec: vec, Model: emb.ModelName(), Dim: emb.Dim()})
}
