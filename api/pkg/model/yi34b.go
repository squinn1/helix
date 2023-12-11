package model

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/lukemarsden/helix/api/pkg/types"
	"github.com/rs/zerolog/log"
)

type Yi34B struct {
	// how many user queries so far (used to calculate <|im_end|> boundary between
	// query and response)
	turns int
}

func (l *Yi34B) GetMemoryRequirements(mode types.SessionMode) uint64 {
	// ATTN TODO: are these right for Yi?
	if mode == types.SessionModeFinetune {
		return GB * 24
	} else {
		return GB * 7
	}
}

func (l *Yi34B) GetType() types.SessionType {
	return types.SessionTypeText
}

func (l *Yi34B) GetTask(session *types.Session) (*types.RunnerTask, error) {
	task, err := getGenericTask(session)
	if err != nil {
		return nil, err
	}

	var turns int
	var messages []string
	// ATTN: this is where the template is defined basically
	for _, interaction := range session.Interactions {
		if interaction.Creator == "user" {
			turns += 1
			messages = append(messages, fmt.Sprintf("<|im_start|>user\n%s<|im_end|>\n", interaction.Message))
		} else {
			messages = append(messages, fmt.Sprintf("<|im_start|>assistant\n%s<|im_end|>", interaction.Message))
		}
	}

	task.Prompt = strings.Join(messages, "\n") + "\n"
	// remember this because we'll use it to know when to start returning results
	l.turns = turns
	return task, nil
}

func (l *Yi34B) GetTextStreams(mode types.SessionMode, eventHandler WorkerEventHandler) (*TextStream, *TextStream, error) {
	if mode == types.SessionModeInference {
		// this understands the context of each word and keeps state
		// to manage the session output window and emit events
		// via the event handler
		chunker := newYi34bInferenceChunker(eventHandler, yi34bInferenceChunkerOptions{
			// no buffering - send every single word
			bufferSize: 0,
			model:      l,
		})

		// this will get called for each word
		// we have already replaced newlines with "[NEWLINE]"
		stdout := NewTextStream(scanWordsPreserveNewlines, func(chunk string) {
			err := chunker.write(chunk)
			if err != nil {
				log.Error().Msgf("error writing word to yi inference chunker: %s", err)
			}
		})

		return stdout, nil, nil
	} else if mode == types.SessionModeFinetune {
		chunker := newYi34bFinetuneChunker(eventHandler, yi34bFinetuneChunkerOptions{
			progressActivationWord: "[axolotl.train.train:108]",
		})
		stdout := NewTextStream(bufio.ScanWords, func(line string) {
			err := chunker.write(line)
			if err != nil {
				log.Error().Msgf("error writing word to yi inference chunker: %s", err)
			}
		})
		stderr := NewTextStream(bufio.ScanWords, func(line string) {
			err := chunker.write(line)
			if err != nil {
				log.Error().Msgf("error writing word to yi inference chunker: %s", err)
			}
		})
		return stdout, stderr, nil
	}

	return nil, nil, nil
}

func (l *Yi34B) GetCommand(ctx context.Context, sessionFilter types.SessionFilter, config types.RunnerProcessConfig) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	if sessionFilter.Mode == types.SessionModeInference {
		cmd = exec.CommandContext(
			ctx,
			"bash", "runner/venv_command.sh",
			"python", "-u", "-m",
			"axolotl.cli.inference",
			// ATTN TODO: set these to a valid yaml
			"examples/yi/qlora-instruct.yml",
		)
	} else {
		cmd = exec.CommandContext(
			ctx,
			"bash", "runner/venv_command.sh",
			"python", "-u", "-m",
			"axolotl.cli.train",
			// ATTN TODO: set these to a valid yaml
			"examples/yi/qlora-instruct.yml",
		)
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	cmd.Env = []string{
		fmt.Sprintf("APP_FOLDER=%s", path.Clean(path.Join(wd, "..", "axolotl"))),
		fmt.Sprintf("HELIX_NEXT_TASK_URL=%s", config.NextTaskURL),
		fmt.Sprintf("HELIX_INITIAL_SESSION_URL=%s", config.InitialSessionURL),
	}

	return cmd, nil
}

type yi34bInferenceChunkerOptions struct {
	// the max size of our buffer - we emit an event if the buffer get's bigger than this
	bufferSize int
	// need to access turns: how many user requests (used to identify boundary between input and output)
	model *Yi34B
}

type yi34bInferenceChunker struct {
	options   yi34bInferenceChunkerOptions
	sessionID string
	// we keep X bytes in memory before emitting an event for the stream
	bufferStream string
	// the entire response for the session is kept in memory
	// so we can submit a complete result when we are complete with a single session
	bufferSession string
	// this means "have we seen the <|im-end|> so are now into the answer?"
	active       bool
	eventHandler WorkerEventHandler
	turnsSoFar   int
}

func newYi34bInferenceChunker(eventHandler WorkerEventHandler, options yi34bInferenceChunkerOptions) *yi34bInferenceChunker {
	return &yi34bInferenceChunker{
		options:       options,
		sessionID:     "",
		bufferStream:  "",
		bufferSession: "",
		active:        false,
		eventHandler:  eventHandler,
		turnsSoFar:    0,
	}
}

func (chunker *yi34bInferenceChunker) addBuffer(word string) {
	chunker.bufferStream += word + " "
	chunker.bufferSession += word + " "
	if len(chunker.bufferStream) > chunker.options.bufferSize {
		chunker.emitStream()
	}
}

func (chunker *yi34bInferenceChunker) emitStream() {
	chunker.eventHandler(&types.RunnerTaskResponse{
		Type:      types.WorkerTaskResponseTypeStream,
		SessionID: chunker.sessionID,
		Message:   chunker.bufferStream,
	})
	chunker.bufferStream = ""
}

func (chunker *yi34bInferenceChunker) emitResult() {
	chunker.eventHandler(&types.RunnerTaskResponse{
		Type:      types.WorkerTaskResponseTypeResult,
		SessionID: chunker.sessionID,
		Message:   chunker.bufferSession,
	})
	chunker.bufferSession = ""
}

func (chunker *yi34bInferenceChunker) write(word string) error {
	// [SESSION_START]session_id=7d11a9ef-a192-426c-bc8e-6bd2c6364b46
	if strings.HasPrefix(word, "[SESSION_START]") {
		// reset turns count
		// ATTN: ChatML template has 2x as many <|im_end|> tokens than Mistral
		chunker.turnsSoFar = chunker.options.model.turns * 2
		log.Info().Msgf(">>> SESSION_START, turnsSoFar: %d", chunker.turnsSoFar)
		parts := strings.Split(word, "=")
		if len(parts) < 2 {
			// we reset here because we got a session start line with no ID
			// which is very strange
			chunker.reset()
			return fmt.Errorf("invalid session start line: %s", word)
		}
		chunker.sessionID = parts[1]
	} else if strings.HasPrefix(word, "[SESSION_END]") {
		chunker.emitResult()
		chunker.reset()
	} else if chunker.sessionID != "" {
		if chunker.active {
			if strings.HasSuffix(word, "</s>") {
				word = strings.Replace(word, "</s>", "", 1)
			}
			chunker.addBuffer(word)
		} else if strings.HasSuffix(word, "<|im_end|>") {
			chunker.turnsSoFar -= 1
			log.Info().Msgf(">>> <|im_end|>, turnsSoFar: %d", chunker.turnsSoFar)
			if chunker.turnsSoFar <= 0 {
				log.Info().Msgf(">>> ACTIVATE")
				chunker.active = true
			}
		}
	}
	return nil
}

func (chunker *yi34bInferenceChunker) reset() {
	chunker.sessionID = ""
	chunker.bufferStream = ""
	chunker.bufferSession = ""
	chunker.active = false
}

type yi34bFinetuneChunkerOptions struct {
	// if defined - we must wait until we see this word
	// before we start to activate percentages
	// this is because the fine tuning emits percentages
	// before the actual training starts so causes
	// the loading bar to flicker back and forth
	progressActivationWord string
}

type yi34bFinetuneChunker struct {
	sessionID      string
	progressActive bool
	options        yi34bFinetuneChunkerOptions
	eventHandler   WorkerEventHandler
}

func newYi34bFinetuneChunker(eventHandler WorkerEventHandler, options yi34bFinetuneChunkerOptions) *yi34bFinetuneChunker {
	return &yi34bFinetuneChunker{
		sessionID:      "",
		eventHandler:   eventHandler,
		options:        options,
		progressActive: false,
	}
}

func (chunker *yi34bFinetuneChunker) write(word string) error {
	// [SESSION_START]session_id=7d11a9ef-a192-426c-bc8e-6bd2c6364b46
	if strings.HasPrefix(word, "[SESSION_START]") {
		parts := strings.Split(word, "=")
		if len(parts) < 2 {
			// we reset here because we got a session start line with no ID
			// which is very strange
			return fmt.Errorf("invalid session start line: %s", word)
		}
		chunker.sessionID = parts[1]
	} else if strings.HasPrefix(word, "[SESSION_END_LORA_DIR]") {
		// e.g. [SESSION_END_LORA_DIR]lora_dir=/tmp/helix/results/123
		parts := strings.Split(word, "=")
		if len(parts) < 2 {
			// we reset here because we got a session start line with no ID
			// which is very strange
			return fmt.Errorf("invalid session start line: %s", word)
		}
		chunker.eventHandler(&types.RunnerTaskResponse{
			Type:      types.WorkerTaskResponseTypeResult,
			SessionID: chunker.sessionID,
			LoraDir:   parts[1],
			Files:     []string{},
		})
		chunker.reset()
	} else if chunker.sessionID != "" {
		if chunker.options.progressActivationWord != "" && !chunker.progressActive && strings.HasPrefix(word, chunker.options.progressActivationWord) {
			chunker.progressActive = true
		}
		// 10%|█
		if strings.Contains(word, "%|") && (chunker.options.progressActivationWord == "" || chunker.progressActive) {
			parts := strings.Split(word, "%")
			percentStr := parts[0]
			progress, err := strconv.Atoi(percentStr)
			if err != nil {
				return err
			}
			chunker.eventHandler(&types.RunnerTaskResponse{
				Type:      types.WorkerTaskResponseTypeProgress,
				SessionID: chunker.sessionID,
				Progress:  progress,
			})
		}
	}
	return nil
}

func (chunker *yi34bFinetuneChunker) reset() {
	chunker.sessionID = ""
	chunker.progressActive = false
}

// Compile-time interface check:
var _ Model = (*Yi34B)(nil)
