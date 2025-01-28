package schedulerv2

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/helixml/helix/api/pkg/config"
	"github.com/helixml/helix/api/pkg/model"
	"github.com/helixml/helix/api/pkg/scheduler"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/puzpuzpuz/xsync/v3"
	"github.com/rs/zerolog/log"
	"golang.org/x/exp/rand"
)

type Scheduler struct {
	ctx             context.Context
	controller      *RunnerController
	queue           []*scheduler.Workload
	queueMtx        *sync.RWMutex
	queueSize       int
	onSchedulingErr func(work *scheduler.Workload, err error)
	slots           *xsync.MapOf[uuid.UUID, *scheduler.Slot] // Maps slot ID to Slot details.
	modelStaleFunc  scheduler.TimeoutFunc                    // Function to check if models are stale
	slotTimeoutFunc scheduler.TimeoutFunc                    // Function to check if slots have timed out due to error
}

type SchedulerParams struct {
	RunnerController  *RunnerController
	QueueSize         int
	OnSchedulingErr   func(work *scheduler.Workload, err error)
	OnResponseHandler func(ctx context.Context, resp *types.RunnerLLMInferenceResponse) error
}

func NewScheduler(ctx context.Context, serverConfig *config.ServerConfig, params *SchedulerParams) (*Scheduler, error) {
	modelTTL := serverConfig.Providers.Helix.ModelTTL
	if modelTTL == 0 {
		modelTTL = 10 * time.Second
	}
	slotTTL := serverConfig.Providers.Helix.SlotTTL
	if slotTTL == 0 {
		slotTTL = 300 * time.Second
	}
	queueSize := 100
	if params.QueueSize > 0 {
		queueSize = params.QueueSize
	}

	log.Info().Dur("model_stale_time", modelTTL).Dur("slot_timeout", slotTTL).Msg("slot timeouts")

	s := &Scheduler{
		ctx:             ctx,
		controller:      params.RunnerController,
		queueSize:       queueSize,
		queue:           make([]*scheduler.Workload, 0, queueSize),
		queueMtx:        &sync.RWMutex{},
		onSchedulingErr: params.OnSchedulingErr,
		slots:           xsync.NewMapOf[uuid.UUID, *scheduler.Slot](),
		modelStaleFunc:  scheduler.NewTimeoutFunc(modelTTL),
		slotTimeoutFunc: scheduler.NewTimeoutFunc(slotTTL),
	}

	// Start the queue processor
	go s.processQueue(ctx)

	// Start the slot reconciler
	go s.reconcileSlots(ctx)

	return s, nil
}

func (s *Scheduler) Enqueue(work *scheduler.Workload) error {
	s.queueMtx.Lock()
	defer s.queueMtx.Unlock()

	// Check if the work is already in the queue.
	for _, w := range s.queue {
		if w.ID() == work.ID() {
			return fmt.Errorf("work already in queue")
		}
	}

	if len(s.queue) >= s.queueSize {
		return fmt.Errorf("queue is full")
	}

	// Check if the work is a session and has priority
	if work.WorkloadType == scheduler.WorkloadTypeSession {
		if work.Session().Metadata.Priority {
			// Add the work to the front of the queue.
			// Ignoring the order of other priority sessions here to avoid complexity
			s.queue = append([]*scheduler.Workload{work}, s.queue...)
			return nil
		}
	}

	// Queue the work
	s.queue = append(s.queue, work)

	return nil
}

// processQueue runs in a goroutine to processes the queue of requests.
func (s *Scheduler) processQueue(ctx context.Context) {
	log.Debug().Msg("starting queue processor")
	for {
		select {
		case <-ctx.Done():
			return
		default:
			s.processQueueOnce()
			// Sleep for a while to allow others to access the queue
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// reconcileSlots runs in a goroutine to reconcile slots.
// The reason why we do this async is because we don't want to have to check the runner on the hot
// path. When a user makes a request we want to forward it to a warm runner as quickly as possible.
func (s *Scheduler) reconcileSlots(ctx context.Context) {
	log.Debug().Msg("starting slot reconciler")
	for {
		select {
		case <-ctx.Done():
			return
		default:
			s.reconcileSlotsOnce()
			// Sleep for a while to allow others to access the queue
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// reconcileSlotsOnce reconciles slots once.
func (s *Scheduler) reconcileSlotsOnce() {
	// Get all runners
	runnerIDs := s.controller.RunnerIDs()

	// For each runner, check their actual slots against what we think they have
	for _, runnerID := range runnerIDs {
		// Get the actual slots from the runner
		actualSlots, err := s.controller.Slots(runnerID)
		if err != nil {
			log.Error().Err(err).Str("runner_id", runnerID).Msg("failed to get slots from runner")
			continue
		}

		// Create a map of actual slot IDs for quick lookup
		actualSlotMap := make(map[uuid.UUID]bool)
		for _, slot := range actualSlots {
			actualSlotMap[slot.ID] = true
		}

		// Check our slots against the runner's actual slots
		s.slots.Range(func(slotID uuid.UUID, slot *scheduler.Slot) bool {
			// Only check slots for this runner
			if slot.RunnerID != runnerID {
				return true
			}

			// If we think we have a slot that the runner doesn't have
			if !actualSlotMap[slotID] {
				s.slots.Delete(slotID)
			}
			return true
		})

		// Any remaining slots in actualSlotMap are ones the runner has that we don't know about, so
		// delete them
		for slotID := range actualSlotMap {
			log.Warn().
				Str("runner_id", runnerID).
				Str("slot_id", slotID.String()).
				Msg("found slot on runner that scheduler doesn't know about")
			// We could try to recreate the slot here, but it's safer to let the runner clean it up
			// since we don't know its full state
			err = s.controller.DeleteSlot(runnerID, slotID)
			if err != nil {
				log.Error().Err(err).Str("runner_id", runnerID).Str("slot_id", slotID.String()).Msg("failed to delete slot")
			}
		}
	}
}

func (s *Scheduler) processQueueOnce() {
	s.queueMtx.Lock()
	defer s.queueMtx.Unlock()

	// Store jobs that weren't able to be scheduled to re-add to the queue later
	// This is important because there many be workloads that persistently fail to schedule
	// and we don't want to block workloads that can be scheduled from further down the queue
	unscheduledQueue := make([]*scheduler.Workload, 0)

	// Schedule any requests that are currently in the queue.
	for _, work := range s.queue {
		err := s.start(work)
		if err != nil {
			retry, err := scheduler.ErrorHandlingStrategy(err, work)

			// If we can retry, break out of the loop and try again later
			if retry {
				unscheduledQueue = append(unscheduledQueue, work)
				continue
			}

			// If we can't retry, write an error to the request and continue so it takes it off
			// the queue
			s.onSchedulingErr(work, err)
		}
	}
	// Clear processed queue
	s.queue = unscheduledQueue
}

func (s *Scheduler) start(work *scheduler.Workload) error {
	if work == nil {
		return fmt.Errorf("workload is nil")
	}
	// Validate model.
	if _, err := model.GetModel(work.ModelName().String()); err != nil {
		return fmt.Errorf("unable to get model (%s): %v", work.ModelName(), err)
	}

	// Validate session mode.
	if work.Mode() == types.SessionModeNone {
		return fmt.Errorf("session mode isn't set")
	}

	var slot *scheduler.Slot // Holds the slot where the work will be scheduled.

	// TODO(Phil): When runners restart, their slots are lost. But the control plane still has it in
	// memory. So we need some way to reconcile this.

	// Try to find warm slots, which are ready to take new work.
	slots := s.WarmSlots(work)

	// If warm slots are available, select a random one.
	if len(slots) > 0 {
		// TODO(PHIL): This doesn't use the scheduling strategy. That is only used for new models.
		// I should probably refactor this to use the strategy for all scheduling.
		// Randomly select one warm slot from the available warm slots.
		slot = slots[rand.Intn(len(slots))]

		err := s.AllocateSlot(slot.ID, work)
		if err != nil {
			// Return error if unable to allocate work to the warm model.
			return fmt.Errorf("unable to allocate work to a warm model slot (ID: %s, slot runner: %s): %w", slot.ID, slot.RunnerID, err)
		}
	} else {
		// If no warm slots are available, pick a runner to allocate a slot to.

		// TODO(Phil): Test to see if the model can fit in ANY runner
		// TODO(Phil): Implement strategy
		// For now, pick a random runner
		allRunners := s.controller.RunnerIDs()
		if len(allRunners) == 0 {
			return fmt.Errorf("no runners available")
		}
		bestRunnerID := allRunners[rand.Intn(len(allRunners))]
		log.Trace().Str("runner_id", bestRunnerID).Msg("chosen best runner")

		// TODO(Phil): Deletion doesn't appear to be working. Create instance. Run. Restart control
		// plane, says can't find stale slot.

		// Figure out if we have to kill a slot to make room for the new one.
		log.Trace().Str("runner_id", bestRunnerID).Uint64("memory_required", work.Model().GetMemoryRequirements(work.Mode())).Msg("deleting stale slots")
		err := s.DeleteMostStaleStrategy(bestRunnerID, work.Model().GetMemoryRequirements(work.Mode()))
		if err != nil {
			return fmt.Errorf("unable to delete stale slots: %w", err)
		}

		// Create an allocated slot
		slot, err = s.AllocateNewSlot(bestRunnerID, work)
		if err != nil {
			// Return error if unable to allocate a new slot.
			return fmt.Errorf("unable to allocate new work on runner (ID: %s): %w", bestRunnerID, err)
		}
	}

	// Store the work associated with the slot for future deallocation.
	if slot == nil {
		// If the slot is nil, return an error.
		return fmt.Errorf("slot is nil")
	}

	return nil
}

// DeleteMostStaleStrategy iteratively deletes allocated work from stale slots until there is enough
// memory to allocate the new workload.
func (s *Scheduler) DeleteMostStaleStrategy(runnerID string, requiredMem uint64) error {
	for {
		var allSlots []*scheduler.Slot
		s.slots.Range(func(id uuid.UUID, slot *scheduler.Slot) bool {
			if slot.RunnerID == runnerID {
				allSlots = append(allSlots, slot)
			}
			return true
		})
		staleSlots := scheduler.Filter(allSlots, func(slot *scheduler.Slot) bool {
			return slot.IsStale()
		})
		// If there is enough free space on the runner, break out of the loop.
		if requiredMem <= s.controller.FreeMemory(runnerID) {
			break
		}
		// Sort the slots by last activity time
		slices.SortFunc(staleSlots, func(i, j *scheduler.Slot) int {
			return int(i.LastActivityTime.Sub(j.LastActivityTime))
		})
		if len(staleSlots) == 0 {
			return fmt.Errorf("unable to find stale slot to replace")
		}
		// Then delete the most stale slot
		log.Debug().Str("slot_id", staleSlots[0].ID.String()).Msg("deleting stale slot")
		s.slots.Delete(staleSlots[0].ID)
	}
	return nil
}

func (s *Scheduler) WarmSlots(req *scheduler.Workload) []*scheduler.Slot {
	cosyWarm := make([]*scheduler.Slot, 0, s.slots.Size())

	s.slots.Range(func(id uuid.UUID, slot *scheduler.Slot) bool {
		l := log.With().
			Str("slot_id", id.String()).
			Str("req_model_name", req.ModelName().String()).
			Str("slot_model_name", slot.ModelName().String()).
			Str("req_inference_runtime", req.ModelName().InferenceRuntime().String()).
			Str("slot_inference_runtime", slot.ModelName().InferenceRuntime().String()).
			Str("req_lora_dir", req.LoraDir()).
			Str("slot_lora_dir", slot.LoraDir()).
			Logger()

		// If it's not the same model name, skip
		if slot.ModelName() != req.ModelName() {
			l.Trace().Msg("skipping warm slot, model name mismatch")
			return true
		}

		// If it's not the same runtime, skip
		if slot.ModelName().InferenceRuntime() != req.ModelName().InferenceRuntime() {
			l.Trace().Msg("skipping warm slot, inference runtime mismatch")
			return true
		}

		// If the slot is already running another job, skip
		if slot.IsActive() {
			l.Trace().Msg("skipping warm slot, already active")
			return true
		}

		// If the slot is scheduled to run another job, skip
		if slot.IsScheduled() {
			l.Trace().Msg("skipping warm slot, already scheduled")
			return true
		}

		// If it doesn't have the right LoraDir then skip
		if slot.LoraDir() != req.LoraDir() {
			l.Trace().Msg("skipping warm slot, LoraDir mismatch")
			return true
		}

		// Add available slots to the list.
		cosyWarm = append(cosyWarm, slot)
		return true
	})
	return cosyWarm
}

// AllocateSlot assigns a workload to a specific slot, validating the model and slot before scheduling.
func (s *Scheduler) AllocateSlot(slotID uuid.UUID, req *scheduler.Workload) error {
	// Validate model
	if _, err := model.GetModel(req.ModelName().String()); err != nil {
		return fmt.Errorf("unable to get model (%s): %v", req.ModelName(), err)
	}

	// Validate slot
	slot, ok := s.slots.Load(slotID)
	if !ok {
		return fmt.Errorf("slot not found: %s", slot.ID.String())
	}

	// Ensure the slot is not already scheduled or active.
	if slot.IsScheduled() {
		return fmt.Errorf("slot has scheduled work: %s", slot.ID.String())
	}
	if slot.IsActive() {
		return fmt.Errorf("slot already active: %s", slot.ID.String())
	}

	log.Trace().
		Str("runner_id", slot.RunnerID).
		Str("slot_id", slot.ID.String()).
		Str("model_name", slot.ModelName().String()).
		Uint64("total_memory", slot.Memory()).
		Str("request_id", req.ID()).
		Msg("allocating slot")

	// Schedule the slot.
	slot.Schedule()

	// Submit the work to the slot
	slot.Start()
	switch req.WorkloadType {
	case scheduler.WorkloadTypeLLMInferenceRequest:
		err := s.controller.SubmitChatCompletionRequest(slot, req.LLMInferenceRequest())
		if err != nil {
			log.Error().Err(err).Msg("error submitting chat completion request")
		}
	case scheduler.WorkloadTypeSession:
		switch req.Session().Mode {
		case types.SessionModeInference:
			switch req.Session().Type {
			case types.SessionTypeImage:
				err := s.controller.SubmitImageGenerationRequest(slot, req.Session())
				if err != nil {
					log.Error().Err(err).Msg("error submitting text2image request")
				}
			default:
				panic(fmt.Sprintf("not implemented: %s", req.Session().Type))
			}
		default:
			panic(fmt.Sprintf("not implemented: %s", req.Session().Mode))
		}
	}
	slot.Release()

	return nil
}

// AllocateNewSlot creates a new slot for a workload and allocates it to the best available runner.
func (s *Scheduler) AllocateNewSlot(runnerID string, req *scheduler.Workload) (*scheduler.Slot, error) {
	// Create a new slot and schedule the workload.
	slot := scheduler.NewSlot(runnerID, req, s.modelStaleFunc, s.slotTimeoutFunc)
	log.Trace().
		Str("runner_id", slot.RunnerID).
		Str("slot_id", slot.ID.String()).
		Str("model_name", slot.ModelName().String()).
		Uint64("total_memory", slot.Memory()).
		Str("request_id", req.ID()).
		Msg("creating new slot")

	err := s.controller.CreateSlot(slot)
	if err != nil {
		return nil, err
	}

	// Ensure the slot is stored.
	s.slots.Store(slot.ID, slot)

	// Schedule and store the new slot.
	return slot, s.AllocateSlot(slot.ID, req)
}

// RunnerSlots returns all slots associated with a specific runner ID.
func (s *Scheduler) RunnerSlots(id string) []*scheduler.Slot {
	allSlots := scheduler.Values(s.slots)
	// Filter slots to include only those belonging to the specified runner.
	return scheduler.Filter(allSlots, func(s *scheduler.Slot) bool {
		return s.RunnerID == id
	})
}
