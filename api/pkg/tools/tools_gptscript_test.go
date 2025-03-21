package tools

import (
	"context"
	"fmt"

	"github.com/davecgh/go-spew/spew"
	"github.com/helixml/helix/api/pkg/types"
	oai "github.com/sashabaranov/go-openai"
	"go.uber.org/mock/gomock"
)

const echoGPT = `description: Returns back the input of the script
args: input: Any string
echo "${input}"`

func (suite *ActionTestSuite) TestAction_runGPTScriptAction_helloWorld() {
	suite.executor.EXPECT().ExecuteScript(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *types.GptScript) (*types.GptScriptResponse, error) {
			return &types.GptScriptResponse{
				Output: `Hello World!`,
			}, nil
		})

	echoGptScript := &types.Tool{
		Name:        "echo",
		Description: "echo tool, use it when you need to echo back the input",
		ToolType:    types.ToolTypeGPTScript,
		Config: types.ToolConfig{
			GPTScript: &types.ToolGPTScriptConfig{
				Script: echoGPT,
			},
		},
	}

	history := []*types.ToolHistoryMessage{
		{
			Role:    oai.ChatMessageRoleUser,
			Content: "echo back 'Hello World'",
		},
	}

	resp, err := suite.strategy.RunAction(suite.ctx, "session-123", "i-123", echoGptScript, history, "echo")
	suite.NoError(err)

	suite.Assert().Contains(resp.Message, "Hello World")

	spew.Dump(resp)

	fmt.Println("U:", history[0].Content)
	fmt.Println("A:", resp.Message)
}

const truckGPTDescription = `is an intelligent remote system that should be used when getting asking for information about trucks`

const truckGPT = `name: jarvis
description: I'm jarvis, a truck guy.
args: question: The question to ask Jarvis about trucks.

When asked about trucks, respond with "Thanks for asking "${question}", I'm am looking into it and will send you an email once I am done!"`

func (suite *ActionTestSuite) TestAction_runGPTScriptAction_ReceiveInput() {

	suite.executor.EXPECT().ExecuteScript(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ *types.GptScript) (*types.GptScriptResponse, error) {
			return &types.GptScriptResponse{
				Output: `Thanks for asking "can I get info about the volvo truck?", I'm am looking into it and will send you an email once I am done!`,
			}, nil
		})

	echoGptScript := &types.Tool{
		Name:        "jarvis",
		Description: truckGPTDescription,
		ToolType:    types.ToolTypeGPTScript,
		Config: types.ToolConfig{
			GPTScript: &types.ToolGPTScriptConfig{
				Script: truckGPT,
			},
		},
	}

	history := []*types.ToolHistoryMessage{
		{
			Role:    oai.ChatMessageRoleUser,
			Content: "can I get info about the volvo truck?",
		},
	}

	resp, err := suite.strategy.RunAction(suite.ctx, "session-123", "i-123", echoGptScript, history, "echo")
	suite.NoError(err)

	suite.Assert().Contains(resp.Message, `Thanks for asking "can I get info about the volvo truck?", I'm am looking into it and will send you an email once I am done!`)

	spew.Dump(resp)

	fmt.Println("U:", history[0].Content)
	fmt.Println("A:", resp.Message)
}
