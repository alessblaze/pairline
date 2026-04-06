package server

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anish/omegle/backend/golang/internal/handlers"
	"github.com/anish/omegle/backend/golang/internal/middleware"
	appredis "github.com/anish/omegle/backend/golang/internal/redis"
	"github.com/anish/omegle/backend/golang/internal/storage"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Server struct {
	router       *gin.Engine
	db           *storage.Database
	redis        *appredis.Client
	sharedSecret string
	jwtSecret    string
	serviceName  string
	enableAdmin  bool
	enablePublic bool
}

const adminCSRFCookieName = "admin_csrf_token"

func NewServer() *Server {
	return newServer(true, true, "omegle-go-service")
}

func NewPublicServer() *Server {
	return newServer(true, false, "omegle-go-public")
}

func NewAdminServer() *Server {
	return newServer(false, true, "omegle-go-admin")
}

func newServer(enablePublic, enableAdmin bool, serviceName string) *Server {
	db := storage.NewDatabase()
	redisClient := appredis.NewClient()

	sharedSecret := os.Getenv("SHARED_SECRET")
	if sharedSecret == "" {
		log.Fatal("SHARED_SECRET environment variable is required")
	}

	jwtSecret := ""
	if enableAdmin {
		jwtSecret = os.Getenv("JWT_SECRET")
		if jwtSecret == "" {
			log.Fatal("JWT_SECRET environment variable is required")
		}
		if len(jwtSecret) < 32 {
			log.Fatal("JWT_SECRET must be at least 32 characters")
		}
	}

	s := &Server{
		db:           db,
		redis:        redisClient,
		sharedSecret: sharedSecret,
		jwtSecret:    jwtSecret,
		serviceName:  serviceName,
		enableAdmin:  enableAdmin,
		enablePublic: enablePublic,
		router:       gin.New(),
	}

	trustedProxies := trustedProxyCIDRsFromEnv()
	if err := s.router.SetTrustedProxies(trustedProxies); err != nil {
		log.Fatalf("Failed to configure trusted proxies: %v", err)
	}

	if enablePublic || enableAdmin {
		s.syncActiveBansToRedis()
		s.startBanSyncLoop()
	}

	s.setupRoutes()

	if enablePublic {
		handlers.Signaling.Start(redisClient)
	}

	return s
}

func (s *Server) syncActiveBansToRedis() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var bans []storage.Ban
	now := time.Now()

	if err := s.db.GetDB().
		Where("is_active = ? AND (expires_at IS NULL OR expires_at > ?)", true, now).
		Find(&bans).Error; err != nil {
		log.Printf("Failed to load active bans for Redis sync: %v", err)
		return
	}

	syncedKeys := 0

	for _, ban := range bans {
		expiration := time.Duration(0)
		if ban.ExpiresAt != nil {
			expiration = time.Until(*ban.ExpiresAt)
			if expiration <= 0 {
				continue
			}
		}

		if ban.SessionID != "" {
			if err := appredis.SetIndexedValue(
				ctx,
				s.redis.GetClient(),
				appredis.BanIndexKey(),
				appredis.BanSessionKey(ban.SessionID),
				ban.Reason,
				expiration,
			); err != nil {
				log.Printf("Failed to sync session ban %s to Redis: %v", ban.SessionID, err)
			} else {
				syncedKeys++
			}
		}

		if ban.IPAddress != "" {
			if err := appredis.SetIndexedValue(
				ctx,
				s.redis.GetClient(),
				appredis.BanIndexKey(),
				appredis.BanIPKey(ban.IPAddress),
				ban.Reason,
				expiration,
			); err != nil {
				log.Printf("Failed to sync IP ban %s to Redis: %v", ban.IPAddress, err)
			} else {
				syncedKeys++
			}
		}
	}

	log.Printf("Synced %d active ban keys to Redis from Postgres", syncedKeys)

	if err := s.reconcileBanKeys(ctx, bans); err != nil {
		log.Printf("Failed reconciling Redis bans: %v", err)
	}
}

func (s *Server) startBanSyncLoop() {
	interval := time.Duration(banSyncIntervalSeconds()) * time.Second
	if interval <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			s.syncActiveBansToRedis()
		}
	}()
}

func banSyncIntervalSeconds() int {
	raw := os.Getenv("BAN_SYNC_INTERVAL_SECONDS")
	if raw == "" {
		return 0
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}

	return value
}

func (s *Server) reconcileBanKeys(ctx context.Context, bans []storage.Ban) error {
	activeKeys := make(map[string]struct{}, len(bans)*2)
	now := time.Now()

	for _, ban := range bans {
		if ban.ExpiresAt != nil && !ban.ExpiresAt.After(now) {
			continue
		}
		if ban.SessionID != "" {
			activeKeys[appredis.BanSessionKey(ban.SessionID)] = struct{}{}
		}
		if ban.IPAddress != "" {
			activeKeys[appredis.BanIPKey(ban.IPAddress)] = struct{}{}
		}
	}

	var cursor uint64
	for {
		keys, nextCursor, err := s.redis.GetClient().SScan(ctx, appredis.BanIndexKey(), cursor, "*", 200).Result()
		if err != nil {
			return err
		}

		staleKeys := make([]string, 0)
		for _, key := range keys {
			if _, ok := activeKeys[key]; !ok {
				staleKeys = append(staleKeys, key)
			}
		}

		if len(staleKeys) > 0 {
			for _, key := range staleKeys {
				if err := appredis.DeleteIndexedKey(ctx, s.redis.GetClient(), appredis.BanIndexKey(), key); err != nil {
					return err
				}
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return nil
}

func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Content-Security-Policy", "default-src 'self'")
		c.Next()
	}
}

func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := uuid.New().String()
		c.Set("request_id", reqID)
		c.Header("X-Request-ID", reqID)
		c.Next()
	}
}

func LimitBodySizeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// Login rate limiter – sliding window, 10 attempts per 15 minutes per IP
// ---------------------------------------------------------------------------

var (
	loginRateLimits  = make(map[string][]time.Time)
	loginRateMu      sync.Mutex
	loginCleanupOnce sync.Once
)

func startLoginRateLimitCleanup() {
	loginCleanupOnce.Do(func() {
		go func() {
			for {
				time.Sleep(5 * time.Minute)
				cutoff := time.Now().Add(-15 * time.Minute)
				loginRateMu.Lock()
				for ip, timestamps := range loginRateLimits {
					var fresh []time.Time
					for _, ts := range timestamps {
						if ts.After(cutoff) {
							fresh = append(fresh, ts)
						}
					}
					if len(fresh) == 0 {
						delete(loginRateLimits, ip)
					} else {
						loginRateLimits[ip] = fresh
					}
				}
				loginRateMu.Unlock()
			}
		}()
	})
}

func LoginRateLimitMiddleware(maxAttempts int, window time.Duration) gin.HandlerFunc {
	startLoginRateLimitCleanup()
	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now()
		cutoff := now.Add(-window)

		loginRateMu.Lock()
		timestamps := loginRateLimits[ip]
		var fresh []time.Time
		for _, ts := range timestamps {
			if ts.After(cutoff) {
				fresh = append(fresh, ts)
			}
		}

		if len(fresh) >= maxAttempts {
			loginRateMu.Unlock()
			c.Header("Retry-After", strconv.Itoa(int(window.Seconds())))
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many login attempts, try again later"})
			c.Abort()
			return
		}

		fresh = append(fresh, now)
		loginRateLimits[ip] = fresh
		loginRateMu.Unlock()

		c.Next()
	}
}

func (s *Server) setupRoutes() {
	s.router.Use(gin.Logger())
	s.router.Use(gin.Recovery())
	s.router.Use(RequestIDMiddleware())
	s.router.Use(SecurityHeadersMiddleware())
	s.router.Use(LimitBodySizeMiddleware())

	allowedOrigins := allowedOriginsFromEnv()

	corsConfig := cors.Config{
		AllowOriginFunc: func(origin string) bool {
			return originAllowed(origin, allowedOrigins)
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "x-signature", "x-timestamp", "x-nonce", "x-csrf-token"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}

	s.router.Use(cors.New(corsConfig))

	s.router.GET("/health", handlers.HealthHandlerGin(s.serviceName))

	if s.enableAdmin {
		admin := s.router.Group("/api/v1/admin")
		{
			admin.POST("/login", LoginRateLimitMiddleware(10, 15*time.Minute), handlers.LoginHandlerGin)

			adminAuth := admin.Group("")
			adminAuth.Use(s.JWTAuthMiddleware())
			adminAuth.Use(AdminCSRFMiddleware(allowedOrigins))
			{
				adminAuth.GET("/reports", handlers.GetReportsHandlerGin)
				adminAuth.PUT("/reports/:id", handlers.UpdateReportHandlerGin)
				adminAuth.GET("/bans", handlers.GetBansHandlerGin)
				adminAuth.POST("/ban", handlers.CreateBanHandlerGin(s.redis))
				adminAuth.DELETE("/ban/:session_id", handlers.DeleteBanHandlerGin(s.redis))

				adminAuth.Use(s.RoleAuthMiddleware([]string{"admin", "root"}))
				adminAuth.POST("/accounts", handlers.CreateAdminHandlerGin)
				adminAuth.DELETE("/accounts/:username", handlers.DeleteAdminHandlerGin)
			}
		}
	}

	if s.enablePublic {
		webrtc := s.router.Group("/api/v1/webrtc")
		{
			webrtc.GET("/ws", handlers.WebRTCWebSocketHandlerGin(s.redis))
			webrtc.POST("/turn", handlers.GetTURNCredentials(s.redis))
		}

		moderation := s.router.Group("/api/v1/moderation")
		{
			moderation.POST("/report", handlers.CreateReportHandlerGin(s.redis.GetClient()))
		}
	}
}

func allowedOriginsFromEnv() []string {
	raw := os.Getenv("CORS_ORIGIN")
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	allowed := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			allowed = append(allowed, trimmed)
		}
	}

	return allowed
}

func originAllowed(origin string, allowedOrigins []string) bool {
	for _, allowed := range allowedOrigins {
		if allowed == origin || allowed+"/" == origin || allowed == origin+"/" {
			return true
		}
	}

	if origin != "" {
		log.Printf("Rejected origin: %s", origin)
	}

	return false
}

func trustedProxyCIDRsFromEnv() []string {
	raw := os.Getenv("TRUSTED_PROXY_CIDRS")
	if raw == "" {
		return []string{"127.0.0.1/32", "::1/128"}
	}

	parts := strings.Split(raw, ",")
	trusted := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			trusted = append(trusted, trimmed)
		}
	}

	return trusted
}

func AdminCSRFMiddleware(allowedOrigins []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}

		if origin := c.GetHeader("Origin"); origin != "" && !originAllowed(origin, allowedOrigins) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invalid request origin"})
			c.Abort()
			return
		}

		cookieToken, err := c.Cookie(adminCSRFCookieName)
		if err != nil || cookieToken == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "CSRF validation failed"})
			c.Abort()
			return
		}

		headerToken := c.GetHeader("X-CSRF-Token")
		if headerToken == "" || subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1 {
			c.JSON(http.StatusForbidden, gin.H{"error": "CSRF validation failed"})
			c.Abort()
			return
		}

		c.Next()
	}
}

func (s *Server) Run(addr string) error {
	server := &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErr := make(chan error, 1)

	go func() {
		log.Printf("Starting server on %s\n", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		return err
	case <-quit:
		log.Println("Graceful shutdown sequence initiated...")
	}

	signal.Stop(quit)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		return err
	}

	// Close DB pool safely
	if err := s.db.Close(); err != nil {
		log.Printf("Error closing DB: %v", err)
	}

	if err := s.redis.Close(); err != nil {
		log.Printf("Error closing Redis: %v", err)
	}

	log.Println("Server exiting properly.")
	return nil
}

func (s *Server) JWTAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			cookie, err := c.Cookie("admin_token")
			if err == nil {
				token = cookie
			}
		}

		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization token required"})
			c.Abort()
			return
		}

		if len(token) > 7 && strings.HasPrefix(token, "Bearer ") {
			token = token[7:]
		}

		username, _, err := middleware.VerifyJWT(token, s.jwtSecret)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Abort()
			return
		}

		var admin storage.AdminAccount
		if err := s.db.GetDB().
			Where("username = ? AND is_active = ?", username, true).
			First(&admin).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Abort()
			return
		}

		c.Set("username", admin.Username)
		c.Set("role", admin.Role)
		c.Next()
	}
}

func (s *Server) RoleAuthMiddleware(allowedRoles []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("role")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}

		roleStr, ok := role.(string)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid role"})
			c.Abort()
			return
		}

		allowed := false
		for _, r := range allowedRoles {
			if r == roleStr {
				allowed = true
				break
			}
		}

		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "Insufficient permissions"})
			c.Abort()
			return
		}

		c.Next()
	}
}
