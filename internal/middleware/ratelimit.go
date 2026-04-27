package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type RateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	r        rate.Limit
	b        int
	ttl      time.Duration
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimitConfig struct {
	RequestsPerSecond float64
	Burst             int
	TTL               time.Duration
	CleanupInterval   time.Duration
}

func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		r:        rate.Limit(cfg.RequestsPerSecond),
		b:        cfg.Burst,
		ttl:      cfg.TTL,
	}

	// Start cleanup goroutine
	go rl.cleanupVisitors(cfg.CleanupInterval)

	return rl
}

func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		limiter := rate.NewLimiter(rl.r, rl.b)
		rl.visitors[ip] = &visitor{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}

	v.lastSeen = time.Now()
	return v.limiter
}

func (rl *RateLimiter) cleanupVisitors(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > rl.ttl {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		limiter := rl.getVisitor(ip)

		// Add rate limit headers
		c.Header("X-RateLimit-Limit", strconv.FormatFloat(float64(rl.r), 'f', 2, 64))
		c.Header("X-RateLimit-Remaining", strconv.Itoa(int(limiter.Tokens())))
		c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))

		if !limiter.Allow() {
			log := GetLogger(c)
			log.Warn().
				Str("client_ip", ip).
				Msg("rate limit exceeded")

			c.Header("Retry-After", "1")
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"message":     "too many requests, please slow down",
				"retry_after": 1,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// Per-endpoint rate limiter for sensitive operations
type EndpointRateLimiter struct {
	limiters map[string]*RateLimiter
	mu       sync.RWMutex
}

func NewEndpointRateLimiter() *EndpointRateLimiter {
	return &EndpointRateLimiter{
		limiters: make(map[string]*RateLimiter),
	}
}

func (erl *EndpointRateLimiter) Limit(endpoint string, cfg RateLimitConfig) gin.HandlerFunc {
	erl.mu.Lock()
	if _, exists := erl.limiters[endpoint]; !exists {
		erl.limiters[endpoint] = NewRateLimiter(cfg)
	}
	rl := erl.limiters[endpoint]
	erl.mu.Unlock()

	return rl.Middleware()
}
