// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2
package handler

import (
	"encoding/json"
	"flowdesk/internal/domain"
	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"fmt"
	"github.com/zeromicro/go-zero/rest/httpx"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const eventPollInterval = 250 * time.Millisecond
const eventHeartbeatInterval = 15 * time.Second
const streamLifetime = 4 * time.Minute
const streamWriteTimeout = 10 * time.Second

func StreamEventsHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.EventsReq
		if err := httpx.Parse(r, &req); err != nil {
			writeBusiness(w, r, 400, nil, &logic.BusinessError{Status: 400, Code: "INVALID_ARGUMENT", Message: "事件游标格式不正确"})
			return
		}
		req.RunId = pathvar.Vars(r)["run_id"]
		if header := r.Header.Get("Last-Event-ID"); header != "" {
			value, err := strconv.ParseInt(header, 10, 64)
			if err != nil {
				writeBusiness(w, r, 400, nil, &logic.BusinessError{Status: 400, Code: "INVALID_ARGUMENT", Message: "事件游标格式不正确"})
				return
			}
			req.AfterSeq = value
		}
		snapshot, err := logic.GetRun(r.Context(), s, req.RunId)
		if err != nil {
			writeBusiness(w, r, 0, nil, err)
			return
		}
		if req.AfterSeq < 0 || req.AfterSeq > snapshot.LastEventSeq {
			writeBusiness(w, r, 400, nil, &logic.BusinessError{Status: 400, Code: "INVALID_ARGUMENT", Message: "事件游标超出范围"})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		controller := http.NewResponseController(w)
		poll := time.NewTicker(eventPollInterval)
		defer poll.Stop()
		started := time.Now()
		heartbeat := started
		authChecked := started
		for {
			if time.Since(authChecked) >= 30*time.Second {
				if _, err := logic.Authenticate(r.Context(), s, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); err != nil {
					_, _ = fmt.Fprint(w, "event: auth.expired\ndata: {\"code\":\"SESSION_EXPIRED\"}\n\n")
					_ = controller.Flush()
					return
				}
				authChecked = time.Now()
			}
			events, err := logic.ListRunEvents(r.Context(), s, req.RunId, req.AfterSeq)
			if err != nil {
				return
			}
			for _, event := range events {
				payload, err := json.Marshal(event)
				if err != nil {
					return
				}
				_ = controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
				if _, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, payload); err != nil {
					return
				}
				if err = controller.Flush(); err != nil {
					return
				}
				req.AfterSeq = event.Seq
			}
			snapshot, err = logic.GetRun(r.Context(), s, req.RunId)
			if err != nil {
				return
			}
			if domain.IsTerminal(snapshot.Status) && req.AfterSeq >= snapshot.LastEventSeq {
				return
			}
			if time.Since(started) >= streamLifetime {
				return
			}
			if time.Since(heartbeat) >= eventHeartbeatInterval {
				_ = controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
				if _, err = fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				if err = controller.Flush(); err != nil {
					return
				}
				heartbeat = time.Now()
			}
			select {
			case <-r.Context().Done():
				return
			case <-poll.C:
			}
		}
	}
}
