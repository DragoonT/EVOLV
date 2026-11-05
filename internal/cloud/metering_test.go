package cloud

import (
	"testing"
	"time"
)

func TestMeterTenantLifecycle(t *testing.T) {
	m := NewMeter()

	tenant := m.RegisterTenant("tenant-1", "Acme Corp", TierPro)
	if tenant.ID != "tenant-1" || tenant.Tier != TierPro || !tenant.Active {
		t.Fatalf("unexpected tenant initialization: %+v", tenant)
	}

	got, err := m.GetTenant("tenant-1")
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if got.Name != "Acme Corp" {
		t.Errorf("got name %q, want Acme Corp", got.Name)
	}

	if err := m.SetTenantStatus("tenant-1", false); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if err := m.AcquireRun("tenant-1"); err != ErrTenantSuspended {
		t.Errorf("got error %v, want ErrTenantSuspended", err)
	}

	_ = m.SetTenantStatus("tenant-1", true)
	if err := m.AcquireRun("tenant-1"); err != nil {
		t.Fatalf("acquire after unsuspend: %v", err)
	}
}

func TestMeterFreeTierQuotasAndConcurrency(t *testing.T) {
	m := NewMeter()
	m.RegisterTenant("free-user", "Hobby Dev", TierFree)

	// 1. First run succeeds
	if err := m.AcquireRun("free-user"); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	// 2. Concurrency limit (Free tier allows 1 concurrent run)
	if err := m.AcquireRun("free-user"); err != ErrConcurrencyLimit {
		t.Errorf("expected ErrConcurrencyLimit, got %v", err)
	}

	// 3. Release run
	m.ReleaseRun("free-user", 500, 250, 1500*time.Millisecond)

	usage, err := m.GetUsage("free-user")
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.RunsCount != 1 {
		t.Errorf("runs count = %d, want 1", usage.RunsCount)
	}
	if usage.ActiveRuns != 0 {
		t.Errorf("active runs = %d, want 0", usage.ActiveRuns)
	}
	if usage.PromptTokens != 500 || usage.CompletionTokens != 250 {
		t.Errorf("tokens mismatch: prompt=%d, completion=%d", usage.PromptTokens, usage.CompletionTokens)
	}
	if usage.ComputeDuration != 1500 {
		t.Errorf("compute duration = %d ms, want 1500", usage.ComputeDuration)
	}
}

func TestMeterMonthlyRunQuotaCutoff(t *testing.T) {
	m := NewMeter()
	m.RegisterTenant("capped-user", "Tester", TierFree)

	limits := DefaultTierLimits(TierFree)

	// Simulate hitting the monthly limit
	for i := int64(0); i < limits.MaxMonthlyRuns; i++ {
		if err := m.AcquireRun("capped-user"); err != nil {
			t.Fatalf("acquire %d failed: %v", i, err)
		}
		m.ReleaseRun("capped-user", 10, 10, 10*time.Millisecond)
	}

	// Next attempt must be blocked by quota
	if err := m.AcquireRun("capped-user"); err != ErrQuotaRunsExceeded {
		t.Errorf("expected ErrQuotaRunsExceeded, got %v", err)
	}
}
