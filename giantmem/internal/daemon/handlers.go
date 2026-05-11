package daemon

import (
	"encoding/json"
	"path/filepath"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/primeinfo"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/sessioninfo"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/statsinfo"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/statusinfo"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/timelineinfo"
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

func (s *Server) handleStatus(req *Request) *Response {
	var p StatusParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errorReply(req.ID, InternalError, "status: bad params: "+err.Error())
		}
	}
	s.mu.RLock()
	archive := s.archiveDB
	live := s.liveDB
	s.mu.RUnlock()

	archiveBase := filepath.Dir(s.archivePath)
	result := statusinfo.Build(archive, live, p.Root, archiveBase, p.Project, p.StaleD)
	return okReply(req.ID, result)
}

func (s *Server) handlePrime(req *Request) *Response {
	var p PrimeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errorReply(req.ID, InternalError, "prime: bad params: "+err.Error())
		}
	}
	if p.Cwd == "" {
		return errorReply(req.ID, InternalError, "prime: cwd is required")
	}
	s.mu.RLock()
	archive := s.archiveDB
	live := s.liveDB
	s.mu.RUnlock()

	archiveBase := filepath.Dir(s.archivePath)
	result, err := primeinfo.Build(archive, live, p.Cwd, archiveBase, p.RecentN, p.SessionN, p.HistoryN)
	if err != nil {
		return errorReply(req.ID, InternalError, "prime: "+err.Error())
	}
	return okReply(req.ID, result)
}

func (s *Server) handleStats(req *Request) *Response {
	s.mu.RLock()
	archive := s.archiveDB
	s.mu.RUnlock()

	rows, err := statsinfo.Query(archive)
	if err != nil {
		return errorReply(req.ID, InternalError, "stats: "+err.Error())
	}
	return okReply(req.ID, map[string]any{"rows": rows})
}

func (s *Server) handleSessionList(req *Request) *Response {
	var p SessionListParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errorReply(req.ID, InternalError, "session.list: bad params: "+err.Error())
		}
	}
	s.mu.RLock()
	archive := s.archiveDB
	s.mu.RUnlock()

	rows, err := sessioninfo.List(archive, p.Project, p.Limit)
	if err != nil {
		return errorReply(req.ID, InternalError, "session.list: "+err.Error())
	}
	return okReply(req.ID, map[string]any{"rows": rows})
}

func (s *Server) handleSessionFind(req *Request) *Response {
	var p SessionFindParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errorReply(req.ID, InternalError, "session.find: bad params: "+err.Error())
		}
	}
	if p.Query == "" {
		return errorReply(req.ID, InternalError, "session.find: query is required")
	}
	s.mu.RLock()
	archive := s.archiveDB
	s.mu.RUnlock()

	rows, err := sessioninfo.Find(archive, p.Query, p.Limit)
	if err != nil {
		return errorReply(req.ID, InternalError, "session.find: "+err.Error())
	}
	return okReply(req.ID, map[string]any{"rows": rows})
}

func (s *Server) handleTimeline(req *Request) *Response {
	var p TimelineParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errorReply(req.ID, InternalError, "timeline: bad params: "+err.Error())
		}
	}
	s.mu.RLock()
	archive := s.archiveDB
	s.mu.RUnlock()

	rows, err := timelineinfo.Query(archive, p.Days, p.Project, p.Source)
	if err != nil {
		return errorReply(req.ID, InternalError, "timeline: "+err.Error())
	}
	return okReply(req.ID, map[string]any{"rows": rows})
}
