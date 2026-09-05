package core

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
)

// fakeSSHD listens on 127.0.0.1, on port, or a free one if port is 0. It
// answers every connection with a real SSH identification banner, so
// sshBannerUp's "SSH-" check passes without a real sshd installed. It
// returns the bound port and a func the caller must defer to shut it down.
func fakeSSHD(t *testing.T, port int) (int, func()) {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = c.Write([]byte("SSH-2.0-fake\r\n"))
				<-done
				_ = c.Close()
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, func() {
		close(done)
		_ = l.Close()
	}
}

func TestWaitUnknownVM(t *testing.T) {
	root(t)
	err := Wait(context.Background(), "nope", UntilStopped)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestWaitStoppedAlreadyStoppedReturnsImmediately(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2300}).Save(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := Wait(context.Background(), "work", UntilStopped); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want near-instant (already stopped)", elapsed)
	}
}

func TestWaitReachableAlreadyUpReturnsImmediately(t *testing.T) {
	root(t)
	port, stop := fakeSSHD(t, 0)
	defer stop()
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: port}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	start := time.Now()
	if err := Wait(context.Background(), "work", UntilReachable); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want near-instant (already reachable)", elapsed)
	}
}

func TestWaitReachablePollsUntilUp(t *testing.T) {
	root(t)
	// Reserve a port, save the VM against it, then only start the fake sshd
	// after a delay; Wait must poll rather than answering on its first
	// check.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: port}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	go func() {
		time.Sleep(2 * pollInterval)
		_, stop := fakeSSHD(t, port)
		defer stop()
		time.Sleep(2 * time.Second)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Wait(ctx, "work", UntilReachable); err != nil {
		t.Fatalf("Wait did not observe the delayed sshd: %v", err)
	}
}

func TestWaitReachableNotRunningReturnsErrCannotReachImmediately(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2301}).Save(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err := Wait(context.Background(), "work", UntilReachable)
	if !errors.Is(err, ErrCannotReach) {
		t.Fatalf("err = %v, want ErrCannotReach", err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want an immediate refusal rather than a block", elapsed)
	}
}

func TestWaitAppliedNoRecipesReturnsErrCannotReachImmediately(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2302}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	start := time.Now()
	err := Wait(context.Background(), "work", UntilApplied)
	if !errors.Is(err, ErrCannotReach) {
		t.Fatalf("err = %v, want ErrCannotReach", err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want an immediate refusal rather than a block", elapsed)
	}
}

func TestWaitAppliedAlreadyDoneReturnsImmediately(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2303, Recipes: []string{"devtools.alpine.sh"}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := writeProvisionLog(v, "waiting for ssh...\n=== recipe devtools.alpine.sh ===\ndone\n"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := Wait(context.Background(), "work", UntilApplied); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want near-instant (already applied)", elapsed)
	}
}

func TestWaitAppliedPollsUntilDoneAppears(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2304, Recipes: []string{"devtools.alpine.sh"}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := writeProvisionLog(v, "waiting for ssh...\n"); err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(2 * pollInterval)
		_ = writeProvisionLog(v, "waiting for ssh...\ndone\n")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Wait(ctx, "work", UntilApplied); err != nil {
		t.Fatalf("Wait did not observe the log turning to done: %v", err)
	}
}

func writeProvisionLog(v *config.VM, content string) error {
	return os.WriteFile(v.ProvisionLogPath(), []byte(content), 0o644)
}

// The cloud-init backend never writes a host apply log (recipe-system-fixes
// design, §3): its recipes run from cloud-init's own runcmd, and
// discoverCloudInitApplied records completion straight into v.Applied over
// ssh. waitApplied's log check can therefore never see "done" for a cloud
// VM; UntilApplied must resolve from v.Applied instead.
func TestWaitAppliedCloudResolvesWhenAllRecipesRecorded(t *testing.T) {
	root(t)
	v := &config.VM{
		Name: "work", Mode: "cloud", RAM: 1024, CPUs: 1, SSHPort: 2310,
		Recipes: []string{"docker"},
		Applied: map[string]config.AppliedRecipe{"docker": {}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := Wait(ctx, "work", UntilApplied); err != nil {
		t.Fatalf("Wait = %v, want nil (docker already recorded applied, no host log exists)", err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want near-instant (already applied)", elapsed)
	}
}

// A recipe missing from v.Applied must keep Wait blocked; UntilApplied
// cannot resolve on a partial match (e.g. any recorded recipe) for a cloud
// VM with more than one recipe configured.
func TestWaitAppliedCloudKeepsWaitingWhileRecipeNotYetApplied(t *testing.T) {
	root(t)
	v := &config.VM{
		Name: "work", Mode: "cloud", RAM: 1024, CPUs: 1, SSHPort: 2311,
		Recipes: []string{"docker", "tailscale"},
		Applied: map[string]config.AppliedRecipe{"docker": {}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := Wait(ctx, "work", UntilApplied)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded (tailscale not yet applied)", err)
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("took %s, want it to actually wait out the 300ms deadline", elapsed)
	}
}

// waitApplied must reload vm.toml on every poll: the discovery that
// populates v.Applied for a cloud VM runs in a separate process (apply),
// so the copy Wait first loaded goes stale the moment that process saves.
func TestWaitAppliedCloudPollsUntilVMTomlRecordsRemainingRecipe(t *testing.T) {
	root(t)
	v := &config.VM{
		Name: "work", Mode: "cloud", RAM: 1024, CPUs: 1, SSHPort: 2312,
		Recipes: []string{"docker", "tailscale"},
		Applied: map[string]config.AppliedRecipe{"docker": {}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(2 * pollInterval)
		v.Applied["tailscale"] = config.AppliedRecipe{}
		_ = v.Save()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Wait(ctx, "work", UntilApplied); err != nil {
		t.Fatalf("Wait did not observe vm.toml recording tailscale applied: %v", err)
	}
}

// Recording a recipe applied in vm.toml must not short-circuit the ssh
// path: it stays log-based, so a stale or missing apply log still blocks
// Wait even when v.Applied already lists every recipe.
func TestWaitAppliedSSHIgnoresAppliedMapWithoutLogDone(t *testing.T) {
	root(t)
	v := &config.VM{
		Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2313,
		Recipes: []string{"devtools.alpine.sh"},
		Applied: map[string]config.AppliedRecipe{"devtools.alpine.sh": {}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := Wait(ctx, "work", UntilApplied)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded (no apply log written yet)", err)
	}
}

func TestWaitStoppedPollsUntilProcessExits(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2305}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	stop := fakeRunning(t, v)

	go func() {
		time.Sleep(2 * pollInterval)
		stop()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Wait(ctx, "work", UntilStopped); err != nil {
		t.Fatalf("Wait did not observe the process exiting: %v", err)
	}
}

// TestWaitCtxCancellationIsPrompt must fail if the ctx.Done() check in
// pollUntil is removed. A VM that is running but never becomes reachable
// would otherwise poll forever. Cancelling ctx right away must return well
// under the test's own timeout, a tripwire for "this would have hung".
func TestWaitCtxCancellationIsPrompt(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2306}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	// No sshd anywhere near this port; UntilReachable can never succeed on
	// its own, so a non-prompt implementation would run until it either
	// busy-loops or hits the ctx deadline far later than cancellation.

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := Wait(ctx, "work", UntilReachable)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took %s to notice cancellation, want well under a second", elapsed)
	}
}

func TestWaitCtxDeadlineExceeded(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2307}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Wait(ctx, "work", UntilReachable)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took %s past a 200ms deadline, want well under a second past it", elapsed)
	}
}

// A VM with no applied recipes that declare health is healthy as soon as ssh
// answers: no later check can change the result.
func TestWaitHealthyWithNoChecksReturnsOnReachable(t *testing.T) {
	root(t)
	port, stopSSH := fakeSSHD(t, 0)
	defer stopSSH()
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: port}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Wait(ctx, v.Name, UntilHealthy); err != nil {
		t.Fatalf("Wait healthy = %v, want nil", err)
	}
}

// The first failing recipe is named and retains the check's last output line,
// so a caller can act on the reported failure rather than a generic timeout.
func TestWaitHealthyNamesFirstFailureAndDetail(t *testing.T) {
	dir := root(t)
	writeHealthRecipe(t, dir, true)
	port, stopSSH := fakeSSHD(t, 0)
	defer stopSSH()
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1,
		SSHPort: port, Recipes: []string{"docker"},
		Applied: map[string]config.AppliedRecipe{"docker": {}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	installHealthSSH(t, false)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Wait(ctx, v.Name, UntilHealthy)
	if err == nil || !strings.Contains(err.Error(), "docker: health check failed") || !strings.Contains(err.Error(), "cannot connect to the docker daemon") {
		t.Fatalf("Wait healthy error = %v, want named check detail", err)
	}
}

// The global healthy deadline honors a declared timeout shorter than the old
// 30s fallback; it must not wait for a separate budget per recipe.
func TestWaitHealthyUsesLongestDeclaredTimeout(t *testing.T) {
	dir := root(t)
	writeHealthRecipeWithTimeout(t, dir, "50ms")
	port, stopSSH := fakeSSHD(t, 0)
	defer stopSSH()
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1,
		SSHPort: port, Recipes: []string{"docker"},
		Applied: map[string]config.AppliedRecipe{"docker": {}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	installHealthSSH(t, false)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := Wait(ctx, v.Name, UntilHealthy)
	if err == nil {
		t.Fatal("Wait healthy succeeded with a failing check")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Wait healthy took %s, want the 50ms health budget", elapsed)
	}
}

// Health checks are evaluated in recipe order, but the caller's one budget is
// the longest declared timeout. A slow first check must not let later checks
// add another full timeout to Wait.
func TestWaitHealthyUsesOneGlobalBudgetForSequentialChecks(t *testing.T) {
	dir := root(t)
	writeHealthRecipeWithCheckTimeoutNamed(t, dir, "health-one", "150ms", "health-one-check")
	writeHealthRecipeWithCheckTimeoutNamed(t, dir, "health-two", "500ms", "health-two-check")
	port, stopSSH := fakeSSHD(t, 0)
	defer stopSSH()
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1,
		SSHPort: port, Recipes: []string{"health-one", "health-two"},
		Applied: map[string]config.AppliedRecipe{"health-one": {}, "health-two": {}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	installSequentialHealthSSH(t, filepath.Join(t.TempDir(), "health-calls"))

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Wait(ctx, v.Name, UntilHealthy)
	if err == nil {
		t.Fatal("Wait healthy succeeded with blocked checks")
	}
	if !strings.Contains(err.Error(), "health-one") || !strings.Contains(err.Error(), "first-health-detail") {
		t.Fatalf("Wait healthy error = %v, want first failing recipe and detail", err)
	}
	if elapsed := time.Since(start); elapsed >= 800*time.Millisecond {
		t.Fatalf("Wait healthy took %s, want one 500ms global budget rather than sequential budgets", elapsed)
	}
}

// A single probe can time out after writing diagnostic output. Wait must keep
// that named, redacted failure instead of returning a bare context deadline.
func TestWaitHealthyRetainsSingleCheckDetailWhenInternalBudgetExpires(t *testing.T) {
	dir := root(t)
	const (
		recipe = "single-blocked"
		secret = "single-health-blocking-secret-7c2"
	)
	writeHealthRecipeWithCheckTimeoutNamed(t, dir, recipe, "100ms", "single-health-check")
	port, stopSSH := fakeSSHD(t, 0)
	defer stopSSH()
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1,
		SSHPort: port, Recipes: []string{recipe},
		Applied: map[string]config.AppliedRecipe{recipe: {}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveSecrets(v.Dir, config.Secrets{recipe: {"token": secret}}); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	installSingleBlockingHealthSSH(t, secret)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := Wait(ctx, v.Name, UntilHealthy)
	if err == nil {
		t.Fatal("Wait healthy succeeded with a single check that exceeded its internal budget")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait healthy returned bare deadline for internal health timeout: %v", err)
	}
	for _, want := range []string{recipe, "single-health-detail", "<redacted>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Wait healthy error = %v, want %q", err, want)
		}
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Wait healthy error leaked stored secret: %v", err)
	}
}

// A child that ignores SIGTERM must still be reaped promptly when a health
// check's context expires. The PID is the fake ssh process itself, so a
// passing implementation cannot leave an owned descendant behind.
func TestHealthCheckReapsTERMIgnoringChildWithinBound(t *testing.T) {
	dir := root(t)
	writeHealthRecipeWithTimeoutNamed(t, dir, "ignore-term", "100ms")
	port, stopSSH := fakeSSHD(t, 0)
	defer stopSSH()
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1,
		SSHPort: port, Recipes: []string{"ignore-term"},
		Applied: map[string]config.AppliedRecipe{"ignore-term": {}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	pidPath := filepath.Join(t.TempDir(), "ssh.pid")
	installIgnoringTERMHealthSSH(t, pidPath)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := HealthChecks(ctx, v.Name)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HealthChecks error = %v, want context deadline", err)
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("HealthChecks took %s after child ignored SIGTERM, want bounded reaping", elapsed)
	}
}

// An accepted TCP peer that never sends an SSH banner is not reachable. The
// banner read must nevertheless observe the caller context instead of waiting
// for its independent two-second socket deadline.
func TestWaitReachableSilentPeerHonorsContext(t *testing.T) {
	root(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	peerDone := make(chan struct{})
	go func() {
		for {
			conn, acceptErr := l.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				<-peerDone
				_ = conn.Close()
			}()
		}
	}()
	v := &config.VM{
		Name: "work", Mode: "live", RAM: 1024, CPUs: 1,
		SSHPort: l.Addr().(*net.TCPAddr).Port,
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = Wait(ctx, v.Name, UntilReachable)
	close(peerDone)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait silent peer error = %v, want context deadline", err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("Wait silent peer took %s, want context-bounded banner read", elapsed)
	}
}

func TestHealthTimeoutUsesLongestDeclaredTimeoutWithoutMinimum(t *testing.T) {
	dir := root(t)
	writeHealthRecipeWithTimeoutNamed(t, dir, "docker", "50ms")
	writeHealthRecipeWithTimeoutNamed(t, dir, "tailscale", "2s")
	v := &config.VM{
		Name: "work", OS: "alpine", Recipes: []string{"docker", "tailscale"},
		Applied: map[string]config.AppliedRecipe{"docker": {}, "tailscale": {}},
	}
	if got, want := HealthTimeout(v), 2*time.Second; got != want {
		t.Fatalf("HealthTimeout = %s, want longest declared timeout %s", got, want)
	}
	writeHealthRecipeWithTimeoutNamed(t, dir, "docker", "50ms")
	v.Applied = map[string]config.AppliedRecipe{"docker": {}}
	if got, want := HealthTimeout(v), 50*time.Millisecond; got != want {
		t.Fatalf("HealthTimeout = %s, want declared timeout %s (not the 30s default)", got, want)
	}
}

// Parent cancellation survives the reachability and health boundaries rather
// than being converted into a recipe failure.
func TestWaitHealthyPropagatesCancellation(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2399}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Wait(ctx, v.Name, UntilHealthy); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait healthy error = %v, want context.Canceled", err)
	}
}

func writeHealthRecipeWithTimeout(t *testing.T, rootDir, timeout string) {
	writeHealthRecipeWithTimeoutNamed(t, rootDir, "docker", timeout)
}

func writeHealthRecipeWithTimeoutNamed(t *testing.T, rootDir, name, timeout string) {
	writeHealthRecipeWithCheckTimeoutNamed(t, rootDir, name, timeout, "docker info")
}

func writeHealthRecipeWithCheckTimeoutNamed(t *testing.T, rootDir, name, timeout, check string) {
	t.Helper()
	d := filepath.Join(rootDir, "recipes", name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema = 3\nname = \"" + name + "\"\nscript = \"install.sh\"\n\n[health]\ncheck = \"" + check + "\"\n"
	if timeout != "" {
		manifest += "timeout = \"" + timeout + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(d, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "install.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func installSequentialHealthSSH(t *testing.T, callsPath string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nbody=$(cat)\ncalls=0\nif [ -f " + shellQuoteCoreTest(callsPath) + " ]; then calls=$(cat " + shellQuoteCoreTest(callsPath) + "); fi\ncalls=$((calls + 1))\nprintf '%s\\n' \"$calls\" > " + shellQuoteCoreTest(callsPath) + "\ncase \"$body\" in\n*health-one-check*) printf '%s\\n' first-health-detail >&2; exit 1;;\n*health-two-check*) if [ \"$calls\" -ge 4 ]; then while :; do :; done; fi; exit 0;;\nesac\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installSingleBlockingHealthSSH(t *testing.T, secret string) {
	t.Helper()
	bin := t.TempDir()
	detail := shellQuoteCoreTest("single-health-detail " + secret)
	script := "#!/bin/sh\nbody=$(cat)\ncase \"$body\" in\n*'single-health-check'*) printf '%s\\n' " + detail + " >&2; trap '' TERM; while :; do :; done;;\nesac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installIgnoringTERMHealthSSH(t *testing.T, pidPath string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' \"$$\" > " + shellQuoteCoreTest(pidPath) + "\ntrap '' TERM\nwhile :; do :; done\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}
