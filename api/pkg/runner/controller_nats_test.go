package runner

import (
	"context"
	"testing"

	"github.com/helixml/helix/api/pkg/controller"
	"github.com/helixml/helix/api/pkg/pubsub"
	"github.com/stretchr/testify/require"
)

func TestControllerDetectsNewRunner(t *testing.T) {
	ps, err := pubsub.NewInMemoryNats()
	require.NoError(t, err)

	testID := "some-id"

	_, err = controller.NewRunnerController(context.Background(), &controller.RunnerControllerConfig{
		PubSub: ps,
		OnConnected: func(id string) {
			require.Equal(t, testID, id)
		},
	})
	require.NoError(t, err)

	_, err = NewNatsController(context.Background(), &NatsControllerConfig{
		PS:        ps,
		ServerURL: "http://localhost:9000",
		RunnerID:  testID,
	})
	require.NoError(t, err)
}
