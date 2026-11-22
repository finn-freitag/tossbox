package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/finn-freitag/tossbox/internal/core"
	"github.com/finn-freitag/tossbox/internal/model"
	"github.com/google/uuid"
)

func newID() string          { return uuid.NewString() }
func validID(id string) bool { _, err := uuid.Parse(id); return err == nil }

// ---------------------------------------------------------------- templates

func (s *Server) templates(w http.ResponseWriter, r *http.Request) error {
	t, err := s.core.Templates(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, t)
}

func (s *Server) reloadTemplates(w http.ResponseWriter, r *http.Request) error {
	n, errs := s.core.ReloadTemplates(r.Context())
	return writeJSON(w, http.StatusOK, map[string]any{"count": n, "errors": errs})
}

func (s *Server) createOptions(w http.ResponseWriter, r *http.Request) error {
	o, err := s.core.CreateOptionsFor(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, o)
}

// ---------------------------------------------------------------- devices

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	all := q.Get("all") != "false"
	d, err := s.core.ListDevices(r.Context(), principal(r), q.Get("owner"), all)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, d)
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) error {
	var req core.CreateDeviceRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	d, err := s.core.CreateDevice(r.Context(), principal(r), req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, d)
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) error {
	d, err := s.core.DeviceView(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, d)
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) error {
	var req core.UpdateDeviceRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	d, err := s.core.UpdateDevice(r.Context(), principal(r), r.PathValue("id"), req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, d)
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) error {
	if err := s.core.DeleteDevice(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

func (s *Server) rdpFile(w http.ResponseWriter, r *http.Request) error {
	name, body, err := s.core.RDPFile(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/x-rdp")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	_, err = w.Write([]byte(body))
	return err
}

func (s *Server) sshConfig(w http.ResponseWriter, r *http.Request) error {
	body, err := s.core.SSHConfig(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = w.Write([]byte(body))
	return err
}

func (s *Server) guacamole(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Service string `json:"service"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			return err
		}
	}
	u, err := s.core.GuacamoleURL(r.Context(), principal(r), r.PathValue("id"), req.Service)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

// ---------------------------------------------------------------- nodes

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) error {
	n, err := s.core.ListNodes(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, n)
}

func (s *Server) createNode(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	res, err := s.core.CreateNode(r.Context(), principal(r), req.Name, req.Address)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, res)
}

func (s *Server) getNode(w http.ResponseWriter, r *http.Request) error {
	n, err := s.core.GetNode(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, n)
}

func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name    *string `json:"name"`
		Address *string `json:"address"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	n, err := s.core.UpdateNode(r.Context(), r.PathValue("id"), req.Name, req.Address)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, n)
}

func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) error {
	if err := s.core.DeleteNode(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) nodeToken(w http.ResponseWriter, r *http.Request) error {
	res, err := s.core.RegenerateNodeToken(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, res)
}

// ---------------------------------------------------------------- users

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) error {
	u, err := s.core.ListUsers(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, u)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) error {
	var req core.CreateUserRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	u, err := s.core.CreateUser(r.Context(), principal(r), req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, u)
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) error {
	u, err := s.core.GetUser(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, u)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) error {
	var req core.UpdateUserRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	u, err := s.core.UpdateUser(r.Context(), principal(r), r.PathValue("id"), req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, u)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) error {
	if err := s.core.DeleteUser(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------- settings

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) error {
	st, err := s.core.Settings(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, st)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) error {
	st, err := s.core.Settings(r.Context())
	if err != nil {
		return err
	}
	// Decoding onto the current settings makes partial updates work.
	if err := decode(r, &st); err != nil {
		return err
	}
	st, err = s.core.UpdateSettings(r.Context(), principal(r), st)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, st)
}

// ---------------------------------------------------------------- SSE

func (s *Server) eventsHandler(w http.ResponseWriter, r *http.Request) error {
	fl, ok := w.(http.Flusher)
	if !ok {
		return model.Errf(model.CodeInternal, "streaming unsupported")
	}
	p := principal(r)
	sub := s.core.Events().Subscribe(p.User.ID, p.IsAdmin())
	defer s.core.Events().Unsubscribe(sub)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n\n")
	fl.Flush()

	keep := time.NewTicker(25 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-keep.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		case ev := <-sub.C:
			data := strings.ReplaceAll(string(ev.Data), "\n", "")
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
			fl.Flush()
		}
	}
}
