// v0002/v0005 state path: team/system-scoped registration, FIFO
// discovery, atomic FIFO claim with stable command_id, task-bound
// environment open via scope token, ordered task events, and Pod
// lifecycle with cleanup delays. The Executor never falls back to
// a local image; it uses tasks.resolved_image verbatim. The cache
// is the durable source of restart reconciliation.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/flowai/platform/executor/k8s-openhands/internal/cache"
	"github.com/flowai/platform/executor/k8s-openhands/internal/k8sclient"
	"github.com/flowai/platform/executor/k8s-openhands/internal/platform"
	"github.com/flowai/platform/executor/k8s-openhands/internal/stateregistryclient"
)

// Reconciler performs the v0005 restart reconciliation. On a
// non-empty cache the Executor re-registers with the same bound
// team_id and authorized_tag, loads claimed non-terminal tasks
// from the bbolt assignments bucket, matches each cached
// owner_command_id to the existing Pod's flowai.command_id label,
// reconnects to the cached OpenHands conversation, and retries
// pending outbox events with their original event_id. No reassignment,
// no re-claim, no duplicate running events.
type Reconciler struct {
	cfg   *Config
	store *cache.Store
	kube  k8sclient.Client
}

// NewReconciler constructs a Reconciler.
func NewReconciler(cfg *Config, store *cache.Store, kube k8sclient.Client) *Reconciler {
	return &Reconciler{cfg: cfg, store: store, kube: kube}
}

// Reconcile returns the non-terminal assignments that match an
// existing Pod in the cluster. Pods that have no matching label
// match (e.g. were deleted by a different actor) are dropped from
// the cache so the Executor does not attempt to reconnect to them.
func (r *Reconciler) Reconcile(ctx context.Context) ([]cache.AssignmentRecord, error) {
	if r.store == nil || r.kube == nil {
		return nil, errors.New("reconciler: missing dependencies")
	}
	all, err := r.store.ListAssignments(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconciler: list assignments: %w", err)
	}
	if r.cfg.Namespace == "" {
		return all, nil
	}
	selector := map[string]string{
		k8sclient.LabelExecutorID: r.cfg.ExecutorID,
		k8sclient.LabelRuntime:    "k8s",
	}
	podStates, err := r.kube.ListMatchingPods(ctx, r.cfg.Namespace, selector)
	if err != nil {
		return nil, fmt.Errorf("reconciler: list pods: %w", err)
	}
	podsByCommand := make(map[string]k8sclient.PodState, len(podStates))
	for _, p := range podStates {
		if cmdID, ok := p.Labels[k8sclient.LabelCommandID]; ok && cmdID != "" {
			podsByCommand[cmdID] = p
		}
	}
	var out []cache.AssignmentRecord
	for _, rec := range all {
		if rec.State == "finished" || rec.State == "failed" {
			continue
		}
		if _, ok := podsByCommand[rec.OwnerCommandID]; ok {
			out = append(out, rec)
		} else {
			// No matching Pod exists; drop the cache record so a
			// future run discovers fresh tasks.
			_ = r.store.DeleteAssignment(ctx, rec.TaskID)
		}
	}
	return out, nil
}

// tickV0002 runs a single poll iteration on the v0002/v0005 state
// path. When a local slot is available it discovers pending tasks
// and claims the oldest eligible; FIFO ordering is enforced inside
// the State Registry, so the client only round-trips on the
// already-sorted result.
func (e *Executor) tickV0002(ctx context.Context) error {
	if e.registry == nil {
		return nil
	}
	hasCapacity, err := e.hasClusterCapacity(ctx)
	if err != nil {
		return err
	}
	if !hasCapacity {
		return nil
	}
	items, err := e.registry.DiscoverTasks(ctx, e.cfg.AuthorizedTag, 100)
	if err != nil {
		if stateregistryclient.IsNotFound(err) {
			return nil
		}
		e.logger("v0002 discover failed: %v", err)
		return err
	}
	if len(items) == 0 {
		return nil
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].IngestedAt.Equal(items[j].IngestedAt) {
			return items[i].TaskID < items[j].TaskID
		}
		return items[i].IngestedAt.Before(items[j].IngestedAt)
	})
	for _, summary := range items {
		hasCapacity, err := e.hasClusterCapacity(ctx)
		if err != nil {
			return err
		}
		if !hasCapacity {
			break
		}
		if e.podFor(summary.TaskID) != nil {
			continue
		}
		if err := e.startTaskV0002(ctx, summary); err != nil {
			e.logger("v0002 start task failed for %s: %v", summary.TaskID, err)
			break
		}
	}
	return nil
}

// hasClusterCapacity treats every Pod still present for this Executor as an
// occupied slot. The in-memory map can shrink before Kubernetes completes a
// deletion (or after a transient OpenHands connection failure), so using it
// alone can over-claim tasks and briefly exceed max_capacity.
func (e *Executor) hasClusterCapacity(ctx context.Context) (bool, error) {
	if e.freeSlots() <= 0 {
		return false, nil
	}
	pods, err := e.kube.ListMatchingPods(ctx, e.cfg.Namespace, map[string]string{
		k8sclient.LabelExecutorID: e.cfg.ExecutorID,
		k8sclient.LabelRuntime:    "k8s",
	})
	if err != nil {
		return false, fmt.Errorf("list executor pods for capacity: %w", err)
	}
	return len(pods) < e.cfg.MaxPods, nil
}

func (e *Executor) freeSlots() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.MaxPods - len(e.pods) - e.slotReservations
}

func (e *Executor) podFor(taskID string) *podSlot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pods[taskID]
}

func (e *Executor) addPod(taskID string, slot *podSlot) {
	e.mu.Lock()
	e.pods[taskID] = slot
	e.mu.Unlock()
	e.observePodCount()
}

func (e *Executor) removePod(taskID string) {
	e.mu.Lock()
	_, present := e.pods[taskID]
	delete(e.pods, taskID)
	e.mu.Unlock()
	if !present {
		return
	}
	e.observePodCount()
}

// startTaskV0002 claims the task, opens the environment, and
// creates the Pod. Every step is conditional on the previous
// 200 response; a non-2xx leaves the slot free and no Pod is
// created.
func (e *Executor) startTaskV0002(ctx context.Context, summary platform.V0002TaskSummary) error {
	taskID := summary.TaskID
	teamID := summary.TeamID
	commandID := e.commandIDFor(taskID)

	// 1. Persist a durable claim intent BEFORE the HTTP claim so an
	// uncertain response can be retried with the identical
	// (task_id, command_id).
	if e.cache != nil {
		if err := e.cache.PutAssignment(ctx, cache.AssignmentRecord{
			TaskID:         taskID,
			TeamID:         teamID,
			ExecutorID:     e.cfg.ExecutorID,
			OwnerCommandID: commandID,
			State:          "claiming",
			RuntimeKind:    "k8s",
			LastObserved:   time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("persist claim intent: %w", err)
		}
	}

	claim, err := e.registry.ClaimTask(ctx, platform.V0002ClaimRequest{
		TaskID:    taskID,
		CommandID: commandID,
	})
	if err != nil {
		switch {
		case stateregistryclient.IsOlderTaskMustBeClaimedFirst(err),
			stateregistryclient.IsTaskAlreadyClaimed(err):
			return nil
		case stateregistryclient.IsNotFound(err):
			return nil
		default:
			return fmt.Errorf("claim %s: %w", taskID, err)
		}
	}
	if claim == nil || claim.Claim != "claimed" {
		return fmt.Errorf("claim %s: unexpected envelope", taskID)
	}

	podEnv := openHandsPodEnv(e.cfg.OpenHandsAPIKey)
	if claim.EnvironmentID != nil {
		if claim.ScopeToken == nil || *claim.ScopeToken == "" {
			return fmt.Errorf("claim %s: environment requires scope_token", taskID)
		}
		values, err := e.registry.OpenEnvironment(ctx, *claim.EnvironmentID, taskID, *claim.ScopeToken)
		if err != nil {
			return fmt.Errorf("open environment for task %s: %w", taskID, err)
		}
		for key, value := range values {
			if key != "SESSION_API_KEY" {
				podEnv[key] = value
			}
		}
	}

	resolvedImage := ""
	var imageTag, imageDigest string
	if claim.ResolvedImage != nil {
		resolvedImage = claim.ResolvedImage.Repository
		imageTag = claim.ResolvedImage.Tag
		imageDigest = claim.ResolvedImage.Digest
	} else if claim.Task.ResolvedImage != nil {
		resolvedImage = claim.Task.ResolvedImage.Repository
		imageTag = claim.Task.ResolvedImage.Tag
		imageDigest = claim.Task.ResolvedImage.Digest
	}
	if resolvedImage == "" {
		return fmt.Errorf("claim %s: missing resolved_image", taskID)
	}
	imageRef := resolvedImage
	if imageRefAlreadyHasTagOrDigest(imageRef) {
		// Caller-supplied reference already encodes a tag or
		// digest; use it verbatim to honour the v0005
		// `resolved_image used verbatim` rule.
	} else if imageTag != "" {
		imageRef += ":" + imageTag
	} else if imageDigest != "" {
		imageRef += "@" + imageDigest
	}
	imageSource := ""
	if claim.ImageSource != nil {
		imageSource = *claim.ImageSource
	}

	// Update the durable assignment to claimed before any runtime side effect.
	if e.cache != nil {
		_ = e.cache.PutAssignment(ctx, cache.AssignmentRecord{
			TaskID:         taskID,
			TeamID:         teamID,
			ExecutorID:     e.cfg.ExecutorID,
			OwnerCommandID: commandID,
			ResolvedImage:  imageRef,
			ImageSource:    imageSource,
			EnvironmentID:  stringPtr(claim.EnvironmentID),
			ScopeToken:     stringPtr(claim.ScopeToken),
			State:          "claimed",
			RuntimeKind:    "k8s",
			LastObserved:   time.Now().UTC(),
		})
	}

	// Reserve the local slot BEFORE createPod so a failed start does
	// not allow the next tick to claim another FIFO task.
	e.reservePodSlot()
	slotInstalled := false
	defer func() {
		if !slotInstalled {
			e.releasePodSlotReservation()
		}
	}()

	// Emit the running event BEFORE creating the Pod.
	if err := e.appendTaskEvent(ctx, taskID, teamID, platform.TaskEventTypeRunning, []byte(`{"phase":"running"}`)); err != nil {
		return fmt.Errorf("append running event: %w", err)
	}

	podName := "oh-" + shortID(taskID)
	labels := k8sclient.BuildTaskLabels(e.cfg.ExecutorID, teamID, taskID, commandID, e.cfg.Scope, imageSource)
	ref, err := e.kube.CreatePod(ctx, k8sclient.PodSpec{
		Name:               podName,
		Namespace:          e.cfg.Namespace,
		Image:              imageRef,
		ImagePullPolicy:    e.cfg.ImagePullPolicy,
		ExecutorID:         e.cfg.ExecutorID,
		TaskID:             taskID,
		CommandID:          commandID,
		TeamID:             teamID,
		Scope:              e.cfg.Scope,
		ResolvedImageSrc:   imageSource,
		Runtime:            "k8s",
		ServiceAccountName: e.cfg.ServiceAccount,
		Port:               e.cfg.OpenHandsPort,
		Env:                podEnv,
		Labels:             labels,
	})
	if err != nil {
		if e.cache != nil {
			_ = e.cache.DeleteAssignment(ctx, taskID)
		}
		return fmt.Errorf("create pod %s: %w", podName, err)
	}

	slot := &podSlot{
		taskID:        taskID,
		teamID:        teamID,
		commandID:     commandID,
		podName:       podName,
		podNamespace:  e.cfg.Namespace,
		podUID:        ref.UID,
		resolvedImage: imageRef,
		imageSource:   imageSource,
		lastObserved:  time.Now().UTC(),
		prompt:        promptFromPayload(claim.Task.Payload),
		cancel:        nil,
		doneCh:        make(chan struct{}),
		exitCh:        make(chan struct{}),
	}
	e.addPod(taskID, slot)
	slotInstalled = true
	e.releasePodSlotReservation()

	if e.cache != nil {
		_ = e.cache.PutAssignment(ctx, cache.AssignmentRecord{
			TaskID:         taskID,
			TeamID:         teamID,
			ExecutorID:     e.cfg.ExecutorID,
			OwnerCommandID: commandID,
			ResolvedImage:  imageRef,
			ImageSource:    imageSource,
			EnvironmentID:  stringPtr(claim.EnvironmentID),
			ScopeToken:     stringPtr(claim.ScopeToken),
			RuntimeKind:    "k8s",
			RuntimeRef:     podName + "@" + ref.UID,
			State:          "running",
			LastObserved:   time.Now().UTC(),
		})
	}
	monitorCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	slot.cancel = cancel
	go e.runPodTask(monitorCtx, slot)

	e.logger("k8s pod created for task %s (pod=%s, image=%s)", taskID, podName, imageRef)
	return nil
}

func openHandsPodEnv(apiKey string) map[string]string {
	env := make(map[string]string)
	if apiKey == "" {
		return env
	}
	env["SESSION_API_KEY"] = apiKey
	return env
}

func stringPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (e *Executor) appendTaskEvent(ctx context.Context, taskID, teamID, eventType string, payload []byte) error {
	if e.registry == nil {
		return errors.New("executor: registry not configured")
	}
	eventID := "evt-" + uuid.NewString()
	envelope := platform.V0002TaskEventEnvelope{
		EventID:    eventID,
		TeamID:     teamID,
		TaskID:     taskID,
		ExecutorID: e.cfg.ExecutorID,
		EventType:  eventType,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		Payload:    payload,
	}
	if e.cache != nil {
		if err := e.cache.PutOutbox(ctx, cache.EventOutboxRecord{
			EventID:    eventID,
			TaskID:     taskID,
			ExecutorID: e.cfg.ExecutorID,
			TeamID:     teamID,
			EventType:  eventType,
			OccurredAt: envelope.OccurredAt,
			Payload:    payload,
			State:      "pending",
			UpdatedAt:  time.Now().UTC(),
		}); err != nil {
			e.logger("k8s outbox persist failed: %v", err)
		}
	}
	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, err := e.registry.AppendTaskEvent(postCtx, envelope)
	if err == nil && e.cache != nil {
		_ = e.cache.UpdateOutboxState(ctx, eventID, "accepted")
	}
	return err
}

func (e *Executor) commandIDFor(taskID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if v, ok := e.lastSeen[taskID]; ok && v != 0 {
		return commandIDForStable(uint8(v))
	}
	id := uint8(time.Now().UTC().UnixNano() & 0xff)
	e.lastSeen[taskID] = id
	return commandIDForStable(id)
}

// commandIDForStable returns a stable text command_id keyed by a
// short byte; the underlying discovery loop reuses the same value
// across retries so the State Registry's (task_id, command_id)
// idempotency handler returns the original 200 without appending.
func commandIDForStable(seed uint8) string {
	const charset = "0123456789abcdef"
	ids := make([]byte, 0, 12)
	ids = append(ids, 'c', 'm', 'd', '-')
	for i := 0; i < 12; i++ {
		ids = append(ids, charset[(seed+uint8(i*13))&0xf])
	}
	return string(ids)
}

// shortID returns an 8-char identifier suitable for a Kubernetes
// resource name tail.
func shortID(taskID string) string {
	s := strings.ReplaceAll(taskID, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

func imageRefAlreadyHasTagOrDigest(imageRef string) bool {
	if strings.Contains(imageRef, "@") {
		return true
	}
	if strings.Contains(imageRef, ":") {
		// ":tag" is the only valid form; ignore host:port for this
		// executor which submits a repository segment only.
		lastSlash := strings.LastIndex(imageRef, "/")
		lastColon := strings.LastIndex(imageRef, ":")
		return lastColon > lastSlash
	}
	return false
}

// observePodCount refreshes the State Registry's running_child_count
// observation; the Registry keeps the value informational and never
// gates discovery or claim on it.
func (e *Executor) observePodCount() {
	current := e.PodCount()
	if e.registry == nil {
		return
	}
	body, _ := json.Marshal(map[string]interface{}{
		"runtime":           "k8s",
		"tool":              "openhands",
		"cleanup_id":        CleanupIdentity(),
		"running_pod_count": current,
	})
	var teamPtr *string
	if e.cfg.Scope == platform.ExecutorScopeTeam {
		t := e.cfg.TeamID
		teamPtr = &t
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, _ = e.registry.RegisterExecutor(ctx, stateregistryclient.RegisterExecutorRequest{
		Scope:           e.cfg.Scope,
		TeamID:          teamPtr,
		ExecutorType:    platform.ExecutorTypeK8sOpenHands,
		Identity:        e.cfg.ExecutorID,
		AuthorizedTag:   e.cfg.AuthorizedTag,
		MaxCapacity:     e.cfg.MaxPods,
		RunningCount:    current,
		RuntimeMetadata: body,
	})
	if current == 0 {
		e.setState(StateReady)
	} else {
		e.setState(StateBusy)
	}
}

func (e *Executor) reservePodSlot() {
	e.mu.Lock()
	e.slotReservations++
	e.mu.Unlock()
}

func (e *Executor) releasePodSlotReservation() {
	e.mu.Lock()
	if e.slotReservations > 0 {
		e.slotReservations--
	}
	e.mu.Unlock()
}

// PodCount is the in-process observation of running pods; it is
// informational and never gates discovery or claim.
func (e *Executor) PodCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pods)
}

// Run is a minimal main-loop variant for tests and the in-cluster
// Deployment. The production loop lives in cmd/executor_k8s_openhands
// and adds signal handling and graceful shutdown.
func (e *Executor) Run(ctx context.Context) error {
	if err := e.ensureRegistered(ctx); err != nil {
		e.setState(StateFailed)
		return fmt.Errorf("register executor: %w", err)
	}
	if e.cache != nil {
		if err := e.reconcileCachedWork(ctx); err != nil {
			e.logger("reconciler init warning: %v", err)
			return fmt.Errorf("reconcile cached work: %w", err)
		}
	}
	tick := time.NewTicker(e.cfg.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			e.setState(StateStopped)
			return ctx.Err()
		case <-tick.C:
			if err := e.tickV0002(ctx); err != nil && !errors.Is(err, context.Canceled) {
				e.logger("tick: %v", err)
			}
		}
	}
}

func (e *Executor) reconcileCachedWork(ctx context.Context) error {
	reconciler, err := ReconcilerForExecutor(e)
	if err != nil {
		return err
	}
	assignments, err := reconciler.Reconcile(ctx)
	if err != nil {
		return err
	}
	if err := e.retryPendingOutbox(ctx); err != nil {
		return err
	}
	for _, rec := range assignments {
		parts := strings.SplitN(rec.RuntimeRef, "@", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("cached task %s has invalid pod reference", rec.TaskID)
		}
		slot := &podSlot{
			taskID: rec.TaskID, teamID: rec.TeamID, commandID: rec.OwnerCommandID,
			podName: parts[0], podNamespace: e.cfg.Namespace, podUID: parts[1],
			resolvedImage: rec.ResolvedImage, imageSource: rec.ImageSource,
			conversationID: rec.ConversationID, lastObserved: rec.LastObserved,
			doneCh: make(chan struct{}), exitCh: make(chan struct{}),
		}
		e.addPod(rec.TaskID, slot)
		monitorCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		slot.cancel = cancel
		go e.runPodTask(monitorCtx, slot)
	}
	return nil
}

func (e *Executor) retryPendingOutbox(ctx context.Context) error {
	pending, err := e.cache.PendingOutbox(ctx)
	if err != nil {
		return fmt.Errorf("list pending outbox: %w", err)
	}
	for _, rec := range pending {
		postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		_, postErr := e.registry.AppendTaskEvent(postCtx, platform.V0002TaskEventEnvelope{
			EventID: rec.EventID, TeamID: rec.TeamID, TaskID: rec.TaskID,
			ExecutorID: rec.ExecutorID, EventType: rec.EventType,
			OccurredAt: rec.OccurredAt, Payload: rec.Payload,
		})
		cancel()
		if postErr != nil {
			return fmt.Errorf("retry outbox event %s: %w", rec.EventID, postErr)
		}
		if err := e.cache.UpdateOutboxState(ctx, rec.EventID, "accepted"); err != nil {
			return fmt.Errorf("accept outbox event %s: %w", rec.EventID, err)
		}
	}
	return nil
}

func (e *Executor) ensureRegistered(ctx context.Context) error {
	if e.registry == nil || e.cache == nil {
		return errors.New("registration requires registry and cache")
	}
	e.setState(StateRegistering)
	var teamID *string
	if e.cfg.Scope == platform.ExecutorScopeTeam {
		team := e.cfg.TeamID
		teamID = &team
	}
	metadata, err := json.Marshal(map[string]any{
		"runtime": "k8s", "tool": "openhands", "namespace": e.cfg.Namespace,
	})
	if err != nil {
		return fmt.Errorf("encode runtime metadata: %w", err)
	}
	body := stateregistryclient.RegisterExecutorRequest{
		Scope: e.cfg.Scope, TeamID: teamID,
		ExecutorType:  platform.ExecutorTypeK8sOpenHands,
		AuthorizedTag: e.cfg.AuthorizedTag, MaxCapacity: e.cfg.MaxPods,
		RunningCount: e.PodCount(), RuntimeMetadata: metadata,
	}
	var record *stateregistryclient.ExecutorRecord
	if e.cfg.ExecutorID == "" {
		record, err = e.registry.CreateExecutor(ctx, body)
	} else {
		record, err = e.registry.RegisterExecutor(ctx, body)
	}
	if err != nil {
		return err
	}
	if record.Scope != e.cfg.Scope || record.AuthorizedTag != e.cfg.AuthorizedTag ||
		!sameOptionalTeam(record.TeamID, teamID) {
		return errors.New("registration response binding does not match configuration")
	}
	if e.cfg.ExecutorID != "" && record.ExecutorID != e.cfg.ExecutorID {
		return errors.New("registration response executor_id does not match cache")
	}
	if err := e.cache.PutExecutorID(ctx, record.ExecutorID); err != nil {
		return fmt.Errorf("persist executor_id: %w", err)
	}
	e.mu.Lock()
	e.cfg.ExecutorID = record.ExecutorID
	e.mu.Unlock()
	e.setState(StateReady)
	return nil
}

func sameOptionalTeam(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// ReconcilerForExecutor returns a Reconciler bound to the
// Executor's cache and K8s client. Used both at startup and during
// the main loop.
func ReconcilerForExecutor(e *Executor) (*Reconciler, error) {
	if e.cache == nil || e.kube == nil {
		return nil, errors.New("reconciler: missing cache or k8s client")
	}
	return NewReconciler(e.cfg, e.cache, e.kube), nil
}
