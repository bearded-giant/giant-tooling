package sessions

import (
	"bufio"
	"encoding/json"
	"os"
)

// Usage is one assistant message's token counts, keyed by the API message id.
type Usage struct {
	MessageID string
	TS        string
	Org       string
	Model     string
	Input     int64
	Cache5m   int64
	Cache1h   int64
	CacheRead int64
	Output    int64
}

// ExtractUsage streams a transcript and returns one Usage per API message in
// first-seen order. A message streamed as several lines keeps the last line's
// counts; the org is the latest credential_org attachment before the message.
func ExtractUsage(jsonlPath string) ([]Usage, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Usage
	idx := map[string]int{}
	org := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var rec struct {
			Type       string `json:"type"`
			Timestamp  string `json:"timestamp"`
			RequestID  string `json:"requestId"`
			UUID       string `json:"uuid"`
			Attachment struct {
				Type string `json:"type"`
				Org  string `json:"organizationUuid"`
			} `json:"attachment"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage *struct {
					Input         int64 `json:"input_tokens"`
					CacheCreation int64 `json:"cache_creation_input_tokens"`
					CacheRead     int64 `json:"cache_read_input_tokens"`
					Output        int64 `json:"output_tokens"`
					Breakdown     *struct {
						M5 int64 `json:"ephemeral_5m_input_tokens"`
						H1 int64 `json:"ephemeral_1h_input_tokens"`
					} `json:"cache_creation"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if rec.Type == "attachment" && rec.Attachment.Type == "credential_org" && rec.Attachment.Org != "" {
			org = rec.Attachment.Org
			continue
		}
		u := rec.Message.Usage
		if rec.Type != "assistant" || u == nil || rec.Message.Model == "" || rec.Message.Model == "<synthetic>" {
			continue
		}
		id := rec.Message.ID
		if id == "" {
			id = rec.RequestID
		}
		if id == "" {
			id = rec.UUID
		}
		row := Usage{MessageID: id, TS: rec.Timestamp, Org: org, Model: rec.Message.Model,
			Input: u.Input, CacheRead: u.CacheRead, Output: u.Output}
		if u.Breakdown != nil {
			row.Cache5m, row.Cache1h = u.Breakdown.M5, u.Breakdown.H1
		} else {
			// older transcripts carry only the total; the 5m bucket is the cheaper guess
			row.Cache5m = u.CacheCreation
		}
		if i, ok := idx[id]; ok {
			out[i] = row
		} else {
			idx[id] = len(out)
			out = append(out, row)
		}
	}
	return out, sc.Err()
}
