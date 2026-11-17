package gateway

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"rf4server/internal/config"
	"rf4server/internal/metrics"
	"rf4server/internal/portal"
	"rf4server/internal/proto"
)

// 指标（NewLoginServer 里注册；nil 时为 no-op 计数器）。
var (
	loginRequests prometheus.Counter
	loginIssued   prometheus.Counter
	loginErrors   prometheus.Counter
)

// GreetingUUID 客户端校验的服务端问候（协议常量，实现在 proto.GreetingUUID）。
const GreetingUUID = proto.GreetingUUID

const loginXMLTpl = `<logon success="true"><host>%s</host><port>%d</port>` +
	`<region>%s</region><server>%s</server><userid>%d</userid>` +
	`<token>%s</token><logintoken>%s</logintoken>` +
	`<logintokenexpires>%d</logintokenexpires></logon>`

// legacyLoginXMLTpl 是旧版 CN 客户端在 7515 明文兼容口上要求的响应格式。
// 客户端按 <response> 根节点解析；返回 <logon> 会报 Invalid URI。
const legacyLoginXMLTpl = `<?xml version="1.0" encoding="UTF-8"?>
<response success="true"><token>%s</token><host>%s</host><port>%d</port>` +
	`<region>%s</region><userid>%d</userid><logintoken>%s</logintoken>` +
	`<logintokenexpires>%d</logintokenexpires></response>`

// LoginServer 登录端点：账号登录、已签发 token 续期和登录失败均使用客户端 XML 格式。
type LoginServer struct {
	cfg       config.Gateway
	store     Store
	log       *slog.Logger
	handler   http.Handler
	srv       *http.Server
	ln        net.Listener
	compatSrv *http.Server
	compatLn  net.Listener
}

// NewLoginServer 构建登录服务（含观测指标注册）。
func NewLoginServer(cfg config.Gateway, store Store, log *slog.Logger, obs *metrics.Server) (*LoginServer, error) {
	s := &LoginServer{cfg: cfg, store: store, log: log}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("加载 TLS 证书: %w", err)
	}
	// RF4 CN 客户端会把上游 URL 原样编码进路径，例如：
	// /lv54/https://api.rf4game.ru/login.php。http.ServeMux 会把其中的
	// 双斜杠清理为单斜杠并返回 307，客户端随后会因 URI 被破坏而登录失败。
	// 登录服务本来就接受任意路径，因此直接使用 HandlerFunc，保留 RequestURI。
	s.handler = http.HandlerFunc(s.handleLogin)
	if storage, ok := store.(*SQLiteStore); ok {
		accountPortal := portal.New(storage.DB)
		s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/portal/") {
				accountPortal.ServeHTTP(w, r)
				return
			}
			s.handleLogin(w, r)
		})
	}
	s.srv = &http.Server{
		Addr:              cfg.LoginAddr,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		},
	}
	if cfg.LoginCompatAddr != "" {
		s.compatSrv = &http.Server{
			Addr:              cfg.LoginCompatAddr,
			Handler:           s.handler,
			ReadHeaderTimeout: 10 * time.Second,
		}
	}
	// 指标（无观测端口时用本地一次性注册表，避免 nil 指标 panic）
	if obs != nil {
		reg := obs.Registry()
		loginRequests = metrics.NewCounter(reg, "gateway_login_requests_total", "登录请求数")
		loginIssued = metrics.NewCounter(reg, "gateway_tokens_issued_total", "签发 token 数")
		loginErrors = metrics.NewCounter(reg, "gateway_login_errors_total", "登录处理错误数")
	} else {
		reg := prometheus.NewRegistry()
		loginRequests = metrics.NewCounter(reg, "gateway_login_requests_total", "登录请求数")
		loginIssued = metrics.NewCounter(reg, "gateway_tokens_issued_total", "签发 token 数")
		loginErrors = metrics.NewCounter(reg, "gateway_login_errors_total", "登录处理错误数")
	}
	return s, nil
}

// Start 创建 TLS listener 并开始服务；返回错误 channel 与绑定错误。
func (s *LoginServer) Start() (<-chan error, error) {
	ln, err := net.Listen("tcp", s.cfg.LoginAddr)
	if err != nil {
		return nil, fmt.Errorf("登录服务监听 %s: %w", s.cfg.LoginAddr, err)
	}
	s.ln = tls.NewListener(ln, s.srv.TLSConfig)
	errCh := make(chan error, 2)
	go func() {
		if err := s.srv.Serve(s.ln); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("登录服务 %s: %w", s.cfg.LoginAddr, err)
		}
	}()
	if s.compatSrv != nil {
		compatLn, err := net.Listen("tcp", s.cfg.LoginCompatAddr)
		if err != nil {
			_ = s.ln.Close()
			return nil, fmt.Errorf("登录兼容口监听 %s: %w", s.cfg.LoginCompatAddr, err)
		}
		s.compatLn = compatLn
		go func() {
			if err := s.compatSrv.Serve(s.compatLn); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("登录兼容口 %s: %w", s.cfg.LoginCompatAddr, err)
			}
		}()
	}
	return errCh, nil
}

// Addr 实际监听地址（Start 之后有效）。
func (s *LoginServer) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// CompatAddr 明文 HTTP 兼容口的实际监听地址；未启用时为空。
func (s *LoginServer) CompatAddr() string {
	if s.compatLn == nil {
		return ""
	}
	return s.compatLn.Addr().String()
}

// Shutdown 优雅停机。
func (s *LoginServer) Shutdown(ctx context.Context) error {
	var first error
	if s.compatSrv != nil {
		first = s.compatSrv.Shutdown(ctx)
	}
	if err := s.srv.Shutdown(ctx); first == nil {
		first = err
	}
	return first
}

func (s *LoginServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	loginRequests.Inc()
	info, authMethod, err := s.resolveLoginIdentity(w, r)
	if err != nil {
		loginErrors.Inc()
		if errors.Is(err, errUnsupportedLoginPath) {
			http.NotFound(w, r)
			return
		}
		writeLoginFailure(w, r)
		fields := r.URL.Query()
		if r.Form != nil {
			fields = r.Form
		}
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		reason := "account_unavailable"
		switch {
		case errors.Is(err, errMissingLoginCredentials):
			reason = "missing_credentials"
		case errors.Is(err, sql.ErrNoRows):
			reason = "unknown_or_banned_account"
		case errors.Is(err, portal.ErrInvalidCredentials):
			reason = "invalid_credentials"
		case errors.Is(err, portal.ErrGamePasswordUnavailable):
			reason = "game_password_requires_web_login"
		}
		s.log.Warn("登录拒绝", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr,
			"reason", reason, "fields", strings.Join(keys, ","))
		return
	}
	userid := info.Userid
	info.IssuedAt = time.Now().Unix()
	info.Region = s.cfg.Region
	ttl := time.Duration(s.cfg.TokenTTL) * 24 * time.Hour
	expires := time.Now().Add(ttl).Unix()
	var token, logintoken, xml string
	if legacyLoginFormat(r) {
		// 旧版 CN 客户端会把 token 当 URI 片段使用，不能包含 '|' 或 '/'。
		// 这里沿用旧后端的 32hex token / 48hex logintoken，并登记到同一 SQLite。
		token = NewLegacyToken()
		logintoken = NewLegacyLoginToken()
		if err := s.store.SaveToken(r.Context(), token, info, ttl); err != nil {
			loginErrors.Inc()
			s.log.Error("保存兼容 token 失败", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := s.store.SaveToken(r.Context(), logintoken, info, ttl); err != nil {
			loginErrors.Inc()
			s.log.Error("保存兼容登录 token 失败", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		xml = fmt.Sprintf(legacyLoginXMLTpl,
			token, s.cfg.AdvertiseHost, s.cfg.AdvertisePort,
			s.cfg.Region, userid, logintoken, expires)
	} else {
		token = NewToken(userid)
		if err := s.store.SaveToken(r.Context(), token, info, ttl); err != nil {
			loginErrors.Inc()
			s.log.Error("保存 token 失败", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// HTTPS 作者通道继续使用 4 段 logintoken。
		logintoken = NewLogintoken(userid)
		if err := s.store.SaveToken(r.Context(), logintoken, info, ttl); err != nil {
			loginErrors.Inc()
			s.log.Error("保存登录 token 失败", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		xml = fmt.Sprintf(loginXMLTpl,
			s.cfg.AdvertiseHost, s.cfg.AdvertisePort,
			s.cfg.Region, s.cfg.ServerName, userid,
			token, logintoken, expires)
	}
	loginIssued.Inc()
	if legacyLoginFormat(r) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(xml)))
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml))
	s.log.Info("登录",
		"method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr,
		"auth_method", authMethod, "userid", userid, "region", s.cfg.Region,
		"realtime", s.cfg.AdvertiseHost)
}

var errUnsupportedLoginPath = errors.New("unsupported login path")
var errMissingLoginCredentials = errors.New("missing login credentials")

func (s *LoginServer) resolveLoginIdentity(w http.ResponseWriter, r *http.Request) (TokenInfo, string, error) {
	path := strings.ToLower(r.URL.Path)
	values := r.URL.Query()
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			return TokenInfo{}, "", err
		}
		values = r.Form
	}

	if strings.HasSuffix(path, "/login.php") || path == "/login.php" ||
		strings.HasSuffix(path, "/steam.php") || path == "/steam.php" {
		username, password := values.Get("login"), values.Get("password")
		if username == "" || password == "" {
			return TokenInfo{}, "", errMissingLoginCredentials
		}
		storage, ok := s.store.(*SQLiteStore)
		if !ok {
			return TokenInfo{}, "", portal.ErrInvalidCredentials
		}
		uid, version, err := portal.AuthenticateGame(r.Context(), storage.DB, username, password)
		if err != nil {
			return TokenInfo{}, "", err
		}
		return TokenInfo{Userid: uid, AccountVersion: version}, "account_password", nil
	}

	if strings.HasSuffix(path, "/login_token.php") || path == "/login_token.php" {
		presented := strings.TrimSpace(values.Get("token"))
		if presented == "" {
			return TokenInfo{}, "", portal.ErrInvalidCredentials
		}
		info, ok, err := s.store.LookupToken(r.Context(), presented)
		if err != nil || !ok || info.Userid <= 0 {
			return TokenInfo{}, "", portal.ErrInvalidCredentials
		}
		return info, "token", nil
	}

	return TokenInfo{}, "", errUnsupportedLoginPath
}

// Format selection only changes XML/token encoding; account checks are identical.
func legacyLoginFormat(r *http.Request) bool {
	return r.TLS == nil || r.Header.Get("X-RF4-Login-Format") == "legacy"
}

func writeLoginFailure(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Connection", "close")
	body := `<logon success="false"><error>账号或密码错误</error></logon>`
	if legacyLoginFormat(r) {
		body = `<?xml version="1.0" encoding="UTF-8"?><response success="false"><error>账号或密码错误</error></response>`
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write([]byte(body))
}

// NewToken 生成 4 段 token：hex(32B)|unix|userid|base64(168B)。
// 整串是 RC4 密钥，客户端会在 realtime 握手时回传。
func NewToken(userid int64) string {
	var h [32]byte
	_, _ = rand.Read(h[:])
	var b [168]byte
	_, _ = rand.Read(b[:])
	return strings.Join([]string{
		hex.EncodeToString(h[:]),
		strconv.FormatInt(time.Now().Unix(), 10),
		strconv.FormatInt(userid, 10),
		base64.StdEncoding.EncodeToString(b[:]),
	}, "|")
}

// NewLegacyToken 生成旧版 CN 客户端使用的 32 位十六进制 token。
func NewLegacyToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NewLegacyLoginToken 生成旧版 CN 客户端使用的 48 位十六进制 logintoken。
func NewLegacyLoginToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NewLogintoken 生成登录 XML 使用的短 token，供辅助 realtime 通道认证。
// 该 token 同时作为 RC4 密钥；第三段是玩家 uid。
func NewLogintoken(userid int64) string {
	var s [6]byte
	_, _ = rand.Read(s[:])
	var t [8]byte
	_, _ = rand.Read(t[:])
	return strings.Join([]string{
		"local",
		hex.EncodeToString(s[:]),
		strconv.FormatInt(userid, 10),
		hex.EncodeToString(t[:]),
	}, "|")
}
