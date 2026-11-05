// Package cloud provides multi-tenant orchestration, resource metering, and
// autonomous GitHub SWE bot integration for EVOLV Hosted Cloud SaaS deployments.
package cloud

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Tier identifies the subscription tier of a tenant.
type Tier string

const (
	TierFree       Tier = "free"
	TierPro        Tier = "pro"
	TierEnterprise Tier = "enterprise"
)

// TierLimits specifies usage ceilings for each subscription level.
type TierLimits struct {
	MaxMonthlyRuns   int64         `json:"max_monthly_runs"`
	MaxMonthlyTokens int64         `json:"max_monthly_tokens"`
	MaxConcurrent    int           `json:"max_concurrent"`
	RunTimeout       time.Duration `json:"run_timeout"`
}

// DefaultTierLimits returns the quota configurations for EVOLV Cloud tiers.
func DefaultTierLimits(tier Tier) TierLimits {
	switch tier {
	case TierEnterprise:
		return TierLimits{
			MaxMonthlyRuns:   100000,
			MaxMonthlyTokens: 1000000000,
			MaxConcurrent:    50,
			RunTimeout:       60 * time.Minute,
		}
	case TierPro:
		return TierLimits{
			MaxMonthlyRuns:   500,
			MaxMonthlyTokens: 25000000,
			MaxConcurrent:    5,
			RunTimeout:       20 * time.Minute,
		}
	case TierFree:
		fallthrough
	default:
		return TierLimits{
			MaxMonthlyRuns:   50,
			MaxMonthlyTokens: 2000000,
			MaxConcurrent:    1,
			RunTimeout:       5 * time.Minute,
		}
	}
}

// Tenant represents an authenticated commercial customer or organization.
type Tenant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Tier      Tier      `json:"tier"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// Usage captures cumulative resource consumption for a billing cycle.
type Usage struct {
	Period           string `json:"period"` // YYYY-MM
	RunsCount        int64  `json:"runs_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	ComputeDuration  int64  `json:"compute_duration_ms"`
	ActiveRuns       int    `json:"active_runs"`
}

// TotalTokens returns prompt + completion tokens.
func (u Usage) TotalTokens() int64 {
	return u.PromptTokens + u.CompletionTokens
}

var (
	ErrTenantNotFound    = errors.New("tenant not found")
	ErrTenantSuspended   = errors.New("tenant account is suspended")
	ErrQuotaRunsExceeded = errors.New("monthly run quota exceeded for tier")
	ErrQuotaTokensExceed = errors.New("monthly token quota exceeded for tier")
	ErrConcurrencyLimit  = errors.New("maximum concurrent runs reached for tier")
)

// Meter manages tenant state, resource tracking, and quota enforcement.
type Meter struct {
	mu      sync.RWMutex
	tenants map[string]*Tenant
	usage   map[string]*Usage // key: tenantID:period
}

// NewMeter instantiates a thread-safe usage and billing meter.
func NewMeter() *Meter {
	return &Meter{
		tenants: make(map[string]*Tenant),
		usage:   make(map[string]*Usage),
	}
}

func currentPeriod() string {
	return time.Now().UTC().Format("2006-01")
}

// RegisterTenant adds or updates a tenant.
func (m *Meter) RegisterTenant(id, name string, tier Tier) *Tenant {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant := &Tenant{
		ID:        id,
		Name:      name,
		Tier:      tier,
		Active:    true,
		CreatedAt: time.Now().UTC(),
	}
	m.tenants[id] = tenant
	return tenant
}

// GetTenant returns the tenant by ID.
func (m *Meter) GetTenant(id string) (*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tenant, ok := m.tenants[id]
	if !ok {
		return nil, ErrTenantNotFound
	}
	return tenant, nil
}

// SetTenantStatus enables or suspends a tenant.
func (m *Meter) SetTenantStatus(id string, active bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, ok := m.tenants[id]
	if !ok {
		return ErrTenantNotFound
	}
	tenant.Active = active
	return nil
}

// GetUsage retrieves current period usage for a tenant.
func (m *Meter) GetUsage(tenantID string) (*Usage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.tenants[tenantID]; !ok {
		return nil, ErrTenantNotFound
	}

	key := fmt.Sprintf("%s:%s", tenantID, currentPeriod())
	u, ok := m.usage[key]
	if !ok {
		return &Usage{Period: currentPeriod()}, nil
	}
	copyUsage := *u
	return &copyUsage, nil
}

// AcquireRun verifies tier limits and claims a concurrent run slot.
func (m *Meter) AcquireRun(tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, ok := m.tenants[tenantID]
	if !ok {
		return ErrTenantNotFound
	}
	if !tenant.Active {
		return ErrTenantSuspended
	}

	limits := DefaultTierLimits(tenant.Tier)
	key := fmt.Sprintf("%s:%s", tenantID, currentPeriod())
	u, ok := m.usage[key]
	if !ok {
		u = &Usage{Period: currentPeriod()}
		m.usage[key] = u
	}

	// 1. Check monthly run limit
	if u.RunsCount >= limits.MaxMonthlyRuns {
		return ErrQuotaRunsExceeded
	}

	// 2. Check monthly token limit
	if u.TotalTokens() >= limits.MaxMonthlyTokens {
		return ErrQuotaTokensExceed
	}

	// 3. Check concurrency limit
	if u.ActiveRuns >= limits.MaxConcurrent {
		return ErrConcurrencyLimit
	}

	u.ActiveRuns++
	u.RunsCount++
	return nil
}

// ReleaseRun records the completion of a run and updates token and duration tallies.
func (m *Meter) ReleaseRun(tenantID string, promptTokens, completionTokens int64, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%s", tenantID, currentPeriod())
	u, ok := m.usage[key]
	if !ok {
		return
	}

	if u.ActiveRuns > 0 {
		u.ActiveRuns--
	}
	u.PromptTokens += promptTokens
	u.CompletionTokens += completionTokens
	u.ComputeDuration += duration.Milliseconds()
}
