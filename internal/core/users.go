package core

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/finn-freitag/tossbox/internal/model"
	"github.com/finn-freitag/tossbox/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// UserView is the API representation of a user.
type UserView struct {
	ID        string      `json:"id"`
	Username  string      `json:"username"`
	Role      string      `json:"role"`
	SSHKeys   []string    `json:"ssh_keys"`
	Quota     model.Quota `json:"quota"`
	Usage     model.Usage `json:"usage"`
	CreatedAt time.Time   `json:"created_at"`
}

// HashPassword hashes a password with bcrypt.
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword compares a bcrypt hash with a password.
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func validatePassword(pw string) error {
	if len(pw) < 8 {
		return model.Errf(model.CodeValidation, "password must have at least 8 characters")
	}
	if len(pw) > 72 {
		return model.Errf(model.CodeValidation, "password must have at most 72 characters")
	}
	return nil
}

func validateUsername(u string) error {
	if len(u) < 2 || len(u) > 64 {
		return model.Errf(model.CodeValidation, "username must have 2-64 characters")
	}
	for _, r := range u {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '@') {
			return model.Errf(model.CodeValidation, "username may only contain letters, digits and - _ . @")
		}
	}
	return nil
}

func validateQuota(q model.Quota) error {
	if q.MaxMachines < 0 || q.MaxVCPUs < 0 || q.MaxRAMMB < 0 || q.MaxStorageGB < 0 || q.MaxPhones < 0 || q.MaxTTLHours < 0 {
		return model.Errf(model.CodeValidation, "quota values must be >= 0")
	}
	for _, e := range q.AllowedExposures {
		if !model.Allows(model.Exposures, e) {
			return model.Errf(model.CodeValidation, "unknown exposure %q", e)
		}
	}
	return nil
}

// UserView returns the view of a user including usage.
func (s *Service) UserView(ctx context.Context, u *store.User) (*UserView, error) {
	usage, err := s.Usage(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &UserView{ID: u.ID, Username: u.Username, Role: u.Role, SSHKeys: u.SSHKeys, Quota: u.Quota.Normalize(), Usage: usage, CreatedAt: u.CreatedAt}, nil
}

// ListUsers returns all users (admin).
func (s *Service) ListUsers(ctx context.Context) ([]*UserView, error) {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	devs, err := s.store.ListDevices(ctx, "")
	if err != nil {
		return nil, err
	}
	byOwner := map[string][]*store.Device{}
	for _, d := range devs {
		byOwner[d.OwnerID] = append(byOwner[d.OwnerID], d)
	}
	out := []*UserView{}
	for _, u := range users {
		out = append(out, &UserView{ID: u.ID, Username: u.Username, Role: u.Role, SSHKeys: u.SSHKeys, Quota: u.Quota, Usage: usageOf(byOwner[u.ID]), CreatedAt: u.CreatedAt})
	}
	return out, nil
}

// CreateUserRequest is the body of POST /api/users.
type CreateUserRequest struct {
	Username string      `json:"username"`
	Password string      `json:"password"`
	Role     string      `json:"role"`
	Quota    model.Quota `json:"quota"`
}

// CreateUser creates a user (admin).
func (s *Service) CreateUser(ctx context.Context, p Principal, req CreateUserRequest) (*UserView, error) {
	req.Username = strings.TrimSpace(req.Username)
	if err := validateUsername(req.Username); err != nil {
		return nil, err
	}
	if err := validatePassword(req.Password); err != nil {
		return nil, err
	}
	if req.Role == "" {
		req.Role = model.RoleUser
	}
	if req.Role != model.RoleUser && req.Role != model.RoleAdmin {
		return nil, model.Errf(model.CodeValidation, "role must be admin or user")
	}
	if err := validateQuota(req.Quota); err != nil {
		return nil, err
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.GetUserByUsername(ctx, req.Username); err == nil {
		return nil, model.Errf(model.CodeConflict, "username already exists")
	}
	u := &store.User{ID: uuid.NewString(), Username: req.Username, PasswordHash: hash, Role: req.Role, Quota: req.Quota}
	if err := s.store.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, model.Errf(model.CodeConflict, "username already exists")
		}
		return nil, err
	}
	s.store.Audit(ctx, p.User.ID, "user.create", u.ID, map[string]any{"username": u.Username, "role": u.Role})
	return s.UserView(ctx, u)
}

// UpdateUserRequest is the body of PATCH /api/users/{id}.
type UpdateUserRequest struct {
	Password *string      `json:"password"`
	Role     *string      `json:"role"`
	Quota    *model.Quota `json:"quota"`
}

// UpdateUser changes password, role or quota (admin).
func (s *Service) UpdateUser(ctx context.Context, p Principal, id string, req UpdateUserRequest) (*UserView, error) {
	u, err := s.getUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Password != nil {
		if err := validatePassword(*req.Password); err != nil {
			return nil, err
		}
		if u.PasswordHash, err = HashPassword(*req.Password); err != nil {
			return nil, err
		}
	}
	if req.Role != nil {
		if *req.Role != model.RoleUser && *req.Role != model.RoleAdmin {
			return nil, model.Errf(model.CodeValidation, "role must be admin or user")
		}
		if u.Role == model.RoleAdmin && *req.Role != model.RoleAdmin {
			if err := s.ensureOtherAdmin(ctx, u.ID); err != nil {
				return nil, err
			}
		}
		u.Role = *req.Role
	}
	if req.Quota != nil {
		if err := validateQuota(*req.Quota); err != nil {
			return nil, err
		}
		u.Quota = *req.Quota
	}
	if err := s.store.UpdateUser(ctx, u); err != nil {
		return nil, err
	}
	if req.Password != nil {
		s.store.DeleteUserSessions(ctx, u.ID, "")
	}
	s.store.Audit(ctx, p.User.ID, "user.update", u.ID, nil)
	return s.UserView(ctx, u)
}

func (s *Service) ensureOtherAdmin(ctx context.Context, exceptID string) error {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return err
	}
	for _, x := range users {
		if x.ID != exceptID && x.Role == model.RoleAdmin {
			return nil
		}
	}
	return model.Errf(model.CodeConflict, "at least one admin must remain")
}

func (s *Service) getUser(ctx context.Context, id string) (*store.User, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, model.Errf(model.CodeNotFound, "user not found")
	}
	u, err := s.store.GetUser(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, model.Errf(model.CodeNotFound, "user not found")
	}
	return u, err
}

// GetUser returns one user (admin).
func (s *Service) GetUser(ctx context.Context, id string) (*UserView, error) {
	u, err := s.getUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.UserView(ctx, u)
}

// DeleteUser deletes a user without devices (admin).
func (s *Service) DeleteUser(ctx context.Context, p Principal, id string) error {
	u, err := s.getUser(ctx, id)
	if err != nil {
		return err
	}
	if u.ID == p.User.ID {
		return model.Errf(model.CodeConflict, "you can't delete yourself")
	}
	if u.Role == model.RoleAdmin {
		if err := s.ensureOtherAdmin(ctx, u.ID); err != nil {
			return err
		}
	}
	if err := s.store.DeleteUser(ctx, id); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return model.Errf(model.CodeConflict, "user still owns devices")
		}
		return err
	}
	s.store.Audit(ctx, p.User.ID, "user.delete", id, map[string]any{"username": u.Username})
	return nil
}

// SetSSHKeys replaces the caller's SSH keys.
func (s *Service) SetSSHKeys(ctx context.Context, p Principal, keys []string) (*UserView, error) {
	clean := []string{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		f := strings.Fields(k)
		if len(f) < 2 || !(strings.HasPrefix(f[0], "ssh-") || strings.HasPrefix(f[0], "ecdsa-") || strings.HasPrefix(f[0], "sk-")) {
			return nil, model.Errf(model.CodeValidation, "invalid SSH public key: %.30s…", k)
		}
		clean = append(clean, k)
	}
	u := p.User
	u.SSHKeys = clean
	if err := s.store.UpdateUser(ctx, u); err != nil {
		return nil, err
	}
	return s.UserView(ctx, u)
}

// ChangePassword changes the caller's password and ends their other sessions.
func (s *Service) ChangePassword(ctx context.Context, p Principal, current, next, keepSessionHash string) error {
	if !CheckPassword(p.User.PasswordHash, current) {
		return model.Errf(model.CodeValidation, "current password is wrong")
	}
	if err := validatePassword(next); err != nil {
		return err
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	p.User.PasswordHash = hash
	if err := s.store.UpdateUser(ctx, p.User); err != nil {
		return err
	}
	return s.store.DeleteUserSessions(ctx, p.User.ID, keepSessionHash)
}

// EnsureAdmin creates the initial admin user if there are no users yet.
func (s *Service) EnsureAdmin(ctx context.Context, username, password string) (bool, error) {
	n, err := s.store.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	if password == "" {
		return false, errors.New("no users exist yet: set TOSSBOX_ADMIN_PASSWORD to create the initial admin")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	return true, s.store.CreateUser(ctx, &store.User{ID: uuid.NewString(), Username: username, PasswordHash: hash, Role: model.RoleAdmin})
}

// Settings returns the current settings.
func (s *Service) Settings(ctx context.Context) (model.Settings, error) {
	return s.store.GetSettings(ctx)
}

// UpdateSettings validates and stores settings (admin).
func (s *Service) UpdateSettings(ctx context.Context, p Principal, st model.Settings) (model.Settings, error) {
	if err := st.Validate(); err != nil {
		return st, err
	}
	if err := s.store.PutSettings(ctx, st); err != nil {
		return st, err
	}
	s.store.Audit(ctx, p.User.ID, "settings.update", "", st)
	return st, nil
}
