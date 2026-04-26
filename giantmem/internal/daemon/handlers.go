package daemon

import (
	"encoding/json"

	"github.com/bryangrimes/gm/internal/search"
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
