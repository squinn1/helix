package runner

import (
	"context"
	"fmt"
	"net/url"

	"github.com/helixml/helix/api/pkg/model"
	"github.com/helixml/helix/api/pkg/types"
	"github.com/rs/zerolog/log"
)

func (r *Runner) pollAxolotlRequests(ctx context.Context) error {
	r.nextGlobalRequestMutex.Lock()
	defer r.nextGlobalRequestMutex.Unlock()

	// Query for the next global inference request
	request, err := r.getNextGlobalSession(ctx)
	if err != nil {
		return err
	}

	if request == nil {
		// Nothing to do
		return nil
	}

	aiModel, err := model.GetModel(request.ModelName)
	if err != nil {
		return fmt.Errorf("error getting model %s: %s", request.ModelName, err.Error())
	}

	// if we need to kill any stale sessions, do it now
	// check for running model instances that have not seen a job in a while
	// and kill them if they are over the timeout AND the session requires it

	// totally ignoring if this errors now, otherwise you will never get to the next request, this
	// is totally fucked.
	r.checkForStaleModelInstances(ctx, aiModel, types.SessionModeFinetune)

	log.Info().
		Str("request_id", request.ID).
		Str("model_name", request.ModelName).
		Msgf("🔵 runner start model instance")

	_, err = r.createAxolotlModelInstance(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to create inference model instance: %w", err)
	}

	return nil
}

func (r *Runner) createAxolotlModelInstance(ctx context.Context, request *types.Session) (*AxolotlModelInstance, error) {
	var (
		modelInstance *AxolotlModelInstance
		err           error
	)

	log.Info().Msg("using Axolotl model instance")
	modelInstance, err = NewAxolotlModelInstance(
		r.Ctx,
		&ModelInstanceConfig{
			InitialSession:  request,
			ResponseHandler: r.handleWorkerResponse,
			GetNextSession: func() (*types.Session, error) {
				r.nextGlobalRequestMutex.Lock()
				defer r.nextGlobalRequestMutex.Unlock()

				queryParams := url.Values{}

				queryParams.Add("model_name", string(modelInstance.Filter().ModelName))
				queryParams.Add("mode", string(modelInstance.Filter().Mode))

				nextRequest, err := r.getNextApiSession(ctx, queryParams)
				if err != nil {
					return nil, err
				}
				return nextRequest, nil
			},
			RunnerOptions: r.Options,
		},
	)
	if err != nil {
		return nil, err
	}

	log.Info().
		Str("model_instance", modelInstance.Filter().ModelName).
		Msgf("🔵 runner started axolotl model instance: %s", modelInstance.ID())

	r.activeModelInstances.Store(modelInstance.ID(), modelInstance)

	err = modelInstance.Start(ctx)
	if err != nil {
		return nil, err
	}

	go func() {
		<-modelInstance.Done()
		log.Debug().
			Msgf("🔵 runner stop axolotl model instance: %s", modelInstance.ID())
		r.activeModelInstances.Delete(modelInstance.ID())
	}()

	return modelInstance, nil
}
