// Package portal serves the player page using the gateway's existing device identity.
package portal

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"rf4server/internal/store"
)

//go:embed web
var assets embed.FS

type session struct {
	uid     int64
	version int64
	expires time.Time
}

type Server struct {
	db       *store.DB
	mu       sync.Mutex
	sessions map[string]session
	mux      *http.ServeMux
}

var ErrInvalidCredentials = errors.New("账号或密码错误，或账号暂不可用")

// New serves account-authenticated player pages; callers cannot choose a UID.
func New(db *store.DB) *Server {
	s := &Server{db: db, sessions: map[string]session{}, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /portal/api/session", s.login)
	s.mux.HandleFunc("DELETE /portal/api/session", s.logout)
	s.mux.HandleFunc("GET /portal/api/profile", s.profile)
	s.mux.HandleFunc("GET /portal/api/membership", s.membership)
	s.mux.HandleFunc("POST /portal/api/membership/purchase", s.purchase)
	s.mux.HandleFunc("/portal/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "接口不存在") })
	files, _ := fs.Sub(assets, "web")
	s.mux.Handle("/portal/", http.StripPrefix("/portal/", http.FileServer(http.FS(files))))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	if r.TLS == nil {
		fail(w, http.StatusBadRequest, "个人中心登录需要 HTTPS")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || (u.Scheme != "https" && u.Scheme != "http") {
			fail(w, 403, "请求来源无效")
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

func write(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || len(req.Username) < 3 || len(req.Username) > 32 || len(req.Password) < 10 || len(req.Password) > 72 {
		fail(w, 400, "账号或密码无效")
		return
	}
	uid, version, err := Authenticate(r.Context(), s.db, req.Username, req.Password)
	if err != nil {
		fail(w, 401, "账号或密码错误，或账号暂不可用")
		return
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		fail(w, 500, "会话创建失败")
		return
	}
	token := hex.EncodeToString(b[:])
	s.mu.Lock()
	for key, value := range s.sessions {
		if !time.Now().Before(value.expires) {
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= 1024 {
		s.mu.Unlock()
		fail(w, 503, "会话较多，请稍后重试")
		return
	}
	s.sessions[token] = session{uid: uid, version: version, expires: time.Now().Add(time.Hour)}
	s.mu.Unlock()
	write(w, map[string]any{"token": token})
}

// Authenticate resolves the administrator-assigned account to its player UID.
func Authenticate(ctx context.Context, db *store.DB, username, password string) (int64, int64, error) {
	account, err := db.PortalAccountByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		return 0, 0, err
	}
	if bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(password)) != nil {
		return 0, 0, ErrInvalidCredentials
	}
	if err := provisionGamePassword(ctx, db, account, password); err != nil {
		return 0, 0, err
	}
	return account.UID, account.Version, nil
}

func (s *Server) identity(w http.ResponseWriter, r *http.Request) (int64, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	value, ok := s.sessions[token]
	if ok && !time.Now().Before(value.expires) {
		delete(s.sessions, token)
		ok = false
	}
	s.mu.Unlock()
	if !ok || !s.db.ValidatePortalSession(r.Context(), value.uid, value.version) {
		s.mu.Lock()
		delete(s.sessions, token)
		s.mu.Unlock()
		fail(w, 401, "账号已失效，请重新登录")
		return 0, false
	}
	return value.uid, true
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.identity(w, r); !ok {
		return
	}
	s.mu.Lock()
	delete(s.sessions, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	s.mu.Unlock()
	write(w, map[string]bool{"ok": true})
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.identity(w, r)
	if !ok {
		return
	}
	p, err := s.db.GetProfile(r.Context(), uid)
	if err != nil {
		fail(w, 500, "角色资料未能读取，请重试")
		return
	}
	m := store.MembershipAt(p.Membership, p.MembershipExp, time.Now().Unix())
	var until any
	if m.ExpiresAt > 0 {
		until = m.ExpiresAt * 1000
	}
	write(w, map[string]any{"userId": uid, "username": p.Name, "nickname": p.Name, "level": p.Level, "stars": p.Stars, "region": p.Region, "premium": m.Active, "premiumUntil": until, "goldMinor": p.Gold, "silverMinor": p.Silver})
}

func (s *Server) membership(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.identity(w, r)
	if !ok {
		return
	}
	state, err := s.db.GetMembershipStatus(r.Context(), uid, time.Now().Unix())
	if err != nil {
		fail(w, 500, "会员状态未能读取，请重试")
		return
	}
	write(w, state)
}

func (s *Server) purchase(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.identity(w, r)
	if !ok {
		return
	}
	var req struct {
		PlanID    string `json:"planID"`
		RequestID string `json:"requestID"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		fail(w, 400, "购买参数无效")
		return
	}
	purchase, err := s.db.PurchaseMembership(r.Context(), uid, req.PlanID, req.RequestID, time.Now().Unix())
	if err != nil {
		var business *store.MembershipError
		if errors.As(err, &business) {
			fail(w, 400, business.Message)
		} else {
			fail(w, 500, "购买未能确认，请重试本次订单")
		}
		return
	}
	state, err := s.db.GetMembershipStatus(r.Context(), uid, time.Now().Unix())
	if err != nil {
		fail(w, 500, "购买已保存，请重试本次订单查询结果")
		return
	}
	write(w, map[string]any{"membership": state.Membership, "balance": state.Balance, "plans": state.Plans, "purchase": purchase})
}
