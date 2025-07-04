package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/sashabaranov/go-openai"

	"github.com/maksymenkoml/lingoose/llm/cache"
	llmobserver "github.com/maksymenkoml/lingoose/llm/observer"
	"github.com/maksymenkoml/lingoose/observer"
	"github.com/maksymenkoml/lingoose/thread"
	"github.com/maksymenkoml/lingoose/tool/llm_with_usage"
	"github.com/maksymenkoml/lingoose/types"
)

const (
	EOS = "\x00"
)

var threadRoleToOpenAIRole = map[thread.Role]string{
	thread.RoleSystem:    "system",
	thread.RoleUser:      "user",
	thread.RoleAssistant: "assistant",
	thread.RoleTool:      "tool",
}

type OpenAI struct {
	openAIClient        *openai.Client
	model               Model
	temperature         float32
	maxTokens           int
	maxCompletionTokens int
	reasoningEffort     string
	stop                []string
	usageCallback       UsageCallback
	functions           map[string]Function
	streamCallbackFn    StreamCallback
	responseFormat      *ResponseFormat
	toolChoice          *string
	cache               *cache.Cache
	Name                string
}

// WithModel sets the model to use for the OpenAI instance.
func (o *OpenAI) WithModel(model Model) *OpenAI {
	o.model = model
	return o
}

// WithTemperature sets the temperature to use for the OpenAI instance.
func (o *OpenAI) WithTemperature(temperature float32) *OpenAI {
	o.temperature = temperature
	return o
}

// WithMaxTokens sets the max tokens to use for the OpenAI instance.
func (o *OpenAI) WithMaxTokens(maxTokens int) *OpenAI {
	o.maxTokens = maxTokens
	return o
}

// WithMaxCompletionTokens sets the max completion tokens to use for the OpenAI instance.
func (o *OpenAI) WithMaxCompletionTokens(maxCompletionTokens int) *OpenAI {
	o.maxCompletionTokens = maxCompletionTokens
	return o
}

// WithReasoningEffort sets the reasoning effort to use for the OpenAI instance.
func (o *OpenAI) WithReasoningEffort(reasoningEffort string) *OpenAI {
	o.reasoningEffort = reasoningEffort
	return o
}

// WithUsageCallback sets the usage callback to use for the OpenAI instance.
func (o *OpenAI) WithUsageCallback(callback UsageCallback) *OpenAI {
	o.usageCallback = callback
	return o
}

// WithStop sets the stop sequences to use for the OpenAI instance.
func (o *OpenAI) WithStop(stop []string) *OpenAI {
	o.stop = stop
	return o
}

// WithClient sets the client to use for the OpenAI instance.
func (o *OpenAI) WithClient(client *openai.Client) *OpenAI {
	o.openAIClient = client
	return o
}

func (o *OpenAI) WithToolChoice(toolChoice *string) *OpenAI {
	o.toolChoice = toolChoice
	return o
}

// WithFunctions sets the functions to use for the OpenAI instance.
func (o *OpenAI) WithFunctions(functions map[string]Function) *OpenAI {
	o.functions = functions
	return o
}

func (o *OpenAI) WithStream(enable bool, callbackFn StreamCallback) *OpenAI {
	if !enable {
		o.streamCallbackFn = nil
	} else {
		o.streamCallbackFn = callbackFn
	}

	return o
}

func (o *OpenAI) WithCache(cache *cache.Cache) *OpenAI {
	o.cache = cache
	return o
}

func (o *OpenAI) WithResponseFormat(responseFormat ResponseFormat) *OpenAI {
	o.responseFormat = &responseFormat
	return o
}

// SetStop sets the stop sequences for the completion.
func (o *OpenAI) SetStop(stop []string) {
	o.stop = stop
}

func (o *OpenAI) setUsageMetadata(usage openai.Usage) {
	callbackMetadata := make(types.Meta)

	err := mapstructure.Decode(usage, &callbackMetadata)
	if err != nil {
		return
	}

	o.usageCallback(callbackMetadata)
}

func New() *OpenAI {
	openAIKey := os.Getenv("OPENAI_API_KEY")

	return &OpenAI{
		openAIClient: openai.NewClient(openAIKey),
		model:        GPT3Dot5Turbo,
		functions:    make(map[string]Function),
		Name:         "openai",
	}
}

func (o *OpenAI) getCache(ctx context.Context, t *thread.Thread) (*cache.Result, error) {
	messages := t.UserQuery()
	cacheQuery := strings.Join(messages, "\n")
	cacheResult, err := o.cache.Get(ctx, cacheQuery)
	if err != nil {
		return cacheResult, err
	}

	t.AddMessage(thread.NewAssistantMessage().AddContent(
		thread.NewTextContent(strings.Join(cacheResult.Answer, "\n")),
	))

	return cacheResult, nil
}

func (o *OpenAI) setCache(ctx context.Context, t *thread.Thread, cacheResult *cache.Result) error {
	lastMessage := t.LastMessage()

	if lastMessage.Role != thread.RoleAssistant || len(lastMessage.Contents) == 0 {
		return nil
	}

	contents := make([]string, 0)
	for _, content := range lastMessage.Contents {
		if content.Type == thread.ContentTypeText {
			contents = append(contents, content.Data.(string))
		} else {
			contents = make([]string, 0)
			break
		}
	}

	err := o.cache.Set(ctx, cacheResult.Embedding, strings.Join(contents, "\n"))
	if err != nil {
		return err
	}

	return nil
}

func (o *OpenAI) Generate(ctx context.Context, t *thread.Thread) error {
	if t == nil {
		return nil
	}

	var err error
	var cacheResult *cache.Result
	if o.cache != nil {
		cacheResult, err = o.getCache(ctx, t)
		if err == nil {
			return nil
		} else if !errors.Is(err, cache.ErrCacheMiss) {
			return fmt.Errorf("%w: %w", ErrOpenAIChat, err)
		}
	}

	chatCompletionRequest := o.buildChatCompletionRequest(t)

	if len(o.functions) > 0 {
		chatCompletionRequest.Tools = o.getChatCompletionRequestTools()
		chatCompletionRequest.ToolChoice = o.getChatCompletionRequestToolChoice()
	}

	generation, err := o.startObserveGeneration(ctx, t)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOpenAIChat, err)
	}

	nMessageBeforeGeneration := len(t.Messages)

	if o.streamCallbackFn != nil {
		err = o.stream(ctx, t, chatCompletionRequest)
	} else {
		err = o.generate(ctx, t, chatCompletionRequest)
	}
	if err != nil {
		return err
	}

	err = o.stopObserveGeneration(ctx, generation, t.Messages[nMessageBeforeGeneration:])
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOpenAIChat, err)
	}

	if o.cache != nil {
		err = o.setCache(ctx, t, cacheResult)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrOpenAIChat, err)
		}
	}

	return nil
}

func (o *OpenAI) GenerateWithUsage(ctx context.Context, t *thread.Thread) (*llm_with_usage.TokensUsage, error) {
	if t == nil {
		return nil, nil
	}

	var err error
	var cacheResult *cache.Result
	if o.cache != nil {
		cacheResult, err = o.getCache(ctx, t)
		if err == nil {
			return nil, nil
		} else if !errors.Is(err, cache.ErrCacheMiss) {
			return nil, fmt.Errorf("%w: %w", ErrOpenAIChat, err)
		}
	}

	chatCompletionRequest := o.buildChatCompletionRequest(t)

	if len(o.functions) > 0 {
		chatCompletionRequest.Tools = o.getChatCompletionRequestTools()
		chatCompletionRequest.ToolChoice = o.getChatCompletionRequestToolChoice()
	}

	generation, err := o.startObserveGeneration(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOpenAIChat, err)
	}

	nMessageBeforeGeneration := len(t.Messages)

	var usage *llm_with_usage.TokensUsage
	if o.streamCallbackFn != nil {
		// Streaming is not supported for GenerateWithUsage yet
		err = o.stream(ctx, t, chatCompletionRequest)
		usage = &llm_with_usage.TokensUsage{} // We can't get accurate token counts from streaming
	} else {
		usage, err = o.generateWithUsage(ctx, t, chatCompletionRequest)
	}
	if err != nil {
		return nil, err
	}

	err = o.stopObserveGeneration(ctx, generation, t.Messages[nMessageBeforeGeneration:])
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOpenAIChat, err)
	}

	if o.cache != nil {
		err = o.setCache(ctx, t, cacheResult)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrOpenAIChat, err)
		}
	}

	return usage, nil
}

func isStreamToolCallResponse(response *openai.ChatCompletionStreamResponse) bool {
	return response.Choices[0].FinishReason == openai.FinishReasonToolCalls || len(response.Choices[0].Delta.ToolCalls) > 0
}

func handleStreamToolCallResponse(
	response *openai.ChatCompletionStreamResponse,
	currentToolCall *openai.ToolCall,
) (updatedToolCall openai.ToolCall, isNewTool bool) {
	if len(response.Choices[0].Delta.ToolCalls) == 0 {
		return
	}
	if response.Choices[0].Delta.ToolCalls[0].ID != "" {
		isNewTool = true
		updatedToolCall = response.Choices[0].Delta.ToolCalls[0]
	} else {
		currentToolCall.Function.Arguments += response.Choices[0].Delta.ToolCalls[0].Function.Arguments
	}
	return
}

func (o *OpenAI) handleEndOfStream(
	messages []*thread.Message,
	content string,
	currentToolCall *openai.ToolCall,
	allToolCalls []openai.ToolCall,
) []*thread.Message {
	o.streamCallbackFn(EOS)
	if len(content) > 0 {
		messages = append(messages, thread.NewAssistantMessage().AddContent(
			thread.NewTextContent(content),
		))
	}
	if currentToolCall.ID != "" {
		allToolCalls = append(allToolCalls, *currentToolCall)
		messages = append(messages, toolCallsToToolCallMessage(allToolCalls))
		messages = append(messages, o.callTools(allToolCalls)...)
	}
	return messages
}

func (o *OpenAI) stream(
	ctx context.Context,
	t *thread.Thread,
	chatCompletionRequest openai.ChatCompletionRequest,
) error {
	stream, err := o.openAIClient.CreateChatCompletionStream(
		ctx,
		chatCompletionRequest,
	)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOpenAIChat, err)
	}

	var content string
	var messages []*thread.Message
	var allToolCalls []openai.ToolCall
	var currentToolCall openai.ToolCall
	for {
		response, errRecv := stream.Recv()
		if errors.Is(errRecv, io.EOF) {
			messages = o.handleEndOfStream(messages, content, &currentToolCall, allToolCalls)
			break
		}

		if len(response.Choices) == 0 {
			return fmt.Errorf("%w: no choices returned", ErrOpenAIChat)
		}

		if isStreamToolCallResponse(&response) {
			updatedToolCall, isNewTool := handleStreamToolCallResponse(&response, &currentToolCall)
			if isNewTool {
				if currentToolCall.ID != "" {
					allToolCalls = append(allToolCalls, currentToolCall)
				}
				currentToolCall = updatedToolCall
			}
		} else {
			content += response.Choices[0].Delta.Content
		}

		o.streamCallbackFn(response.Choices[0].Delta.Content)
	}

	t.AddMessages(messages...)

	return nil
}

func (o *OpenAI) generate(
	ctx context.Context,
	t *thread.Thread,
	chatCompletionRequest openai.ChatCompletionRequest,
) error {
	response, err := o.openAIClient.CreateChatCompletion(
		ctx,
		chatCompletionRequest,
	)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOpenAIChat, err)
	}

	if o.usageCallback != nil {
		o.setUsageMetadata(response.Usage)
	}

	if len(response.Choices) == 0 {
		return fmt.Errorf("%w: no choices returned", ErrOpenAIChat)
	}

	var messages []*thread.Message
	if response.Choices[0].FinishReason == "tool_calls" || len(response.Choices[0].Message.ToolCalls) > 0 {
		messages = append(messages, toolCallsToToolCallMessage(response.Choices[0].Message.ToolCalls))
		messages = append(messages, o.callTools(response.Choices[0].Message.ToolCalls)...)
	} else {
		messages = []*thread.Message{
			thread.NewAssistantMessage().AddContent(
				thread.NewTextContent(response.Choices[0].Message.Content),
			),
		}
	}

	t.Messages = append(t.Messages, messages...)

	return nil
}

// generateWithUsage is similar to the original generate function but returns token usage
func (o *OpenAI) generateWithUsage(
	ctx context.Context,
	t *thread.Thread,
	chatCompletionRequest openai.ChatCompletionRequest,
) (*llm_with_usage.TokensUsage, error) {
	response, err := o.openAIClient.CreateChatCompletion(
		ctx,
		chatCompletionRequest,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOpenAIChat, err)
	}

	if o.usageCallback != nil {
		o.setUsageMetadata(response.Usage)
	}

	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("%w: no choices returned", ErrOpenAIChat)
	}

	var messages []*thread.Message
	if response.Choices[0].FinishReason == "tool_calls" || len(response.Choices[0].Message.ToolCalls) > 0 {
		messages = append(messages, toolCallsToToolCallMessage(response.Choices[0].Message.ToolCalls))
		messages = append(messages, o.callTools(response.Choices[0].Message.ToolCalls)...)
	} else {
		messages = []*thread.Message{
			thread.NewAssistantMessage().AddContent(
				thread.NewTextContent(response.Choices[0].Message.Content),
			),
		}
	}

	t.Messages = append(t.Messages, messages...)

	// Create and return TokensUsage from the response, using the llm_with_usage.TokensUsage type
	usage := &llm_with_usage.TokensUsage{
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		AudioTokens:      0,
		CachedTokens:     0,
	}

	if response.Usage.PromptTokensDetails != nil {
		usage.AudioTokens = response.Usage.PromptTokensDetails.AudioTokens
		usage.CachedTokens = response.Usage.PromptTokensDetails.CachedTokens
	}

	return usage, nil
}

func (o *OpenAI) buildChatCompletionRequest(t *thread.Thread) openai.ChatCompletionRequest {
	var responseFormat *openai.ChatCompletionResponseFormat
	if o.responseFormat != nil {
		responseFormat = &openai.ChatCompletionResponseFormat{
			Type: *o.responseFormat,
		}
	}

	r := openai.ChatCompletionRequest{
		Model:          string(o.model),
		Messages:       threadToChatCompletionMessages(t),
		N:              DefaultOpenAINumResults,
		TopP:           DefaultOpenAITopP,
		Stop:           o.stop,
		ResponseFormat: responseFormat,
	}

	if o.maxTokens > 0 {
		r.MaxTokens = o.maxTokens
	}
	if o.maxCompletionTokens > 0 {
		r.MaxCompletionTokens = o.maxCompletionTokens
	}
	if o.temperature > 0 {
		r.Temperature = o.temperature
	}

	return r
}

func (o *OpenAI) getChatCompletionRequestTools() []openai.Tool {
	tools := []openai.Tool{}

	for _, function := range o.functions {
		tools = append(tools, openai.Tool{
			Type: "function",
			Function: &openai.FunctionDefinition{
				Name:        function.Name,
				Description: function.Description,
				Parameters:  function.Parameters,
			},
		})
	}

	return tools
}

func (o *OpenAI) getChatCompletionRequestToolChoice() any {
	if o.toolChoice == nil {
		return "none"
	}

	if *o.toolChoice == "auto" {
		return "auto"
	}

	return openai.ToolChoice{
		Type: openai.ToolTypeFunction,
		Function: openai.ToolFunction{
			Name: *o.toolChoice,
		},
	}
}

func (o *OpenAI) callTool(toolCall openai.ToolCall) (string, error) {
	fn, ok := o.functions[toolCall.Function.Name]
	if !ok {
		return "", fmt.Errorf("unknown function %s", toolCall.Function.Name)
	}

	resultAsJSON, err := callFnWithArgumentAsJSON(fn.Fn, toolCall.Function.Arguments)
	if err != nil {
		return "", err
	}

	return resultAsJSON, nil
}

func (o *OpenAI) callTools(toolCalls []openai.ToolCall) []*thread.Message {
	if len(o.functions) == 0 || len(toolCalls) == 0 {
		return nil
	}

	var messages []*thread.Message
	for _, toolCall := range toolCalls {
		result, err := o.callTool(toolCall)
		if err != nil {
			result = fmt.Sprintf("error: %s", err)
		}

		messages = append(messages, toolCallResultToThreadMessage(toolCall, result))
	}

	return messages
}

func (o *OpenAI) startObserveGeneration(ctx context.Context, t *thread.Thread) (*observer.Generation, error) {
	return llmobserver.StartObserveGeneration(
		ctx,
		o.Name,
		string(o.model),
		types.M{
			"maxTokens":   o.maxTokens,
			"temperature": o.temperature,
		},
		t,
	)
}

func (o *OpenAI) stopObserveGeneration(
	ctx context.Context,
	generation *observer.Generation,
	messages []*thread.Message,
) error {
	return llmobserver.StopObserveGeneration(
		ctx,
		generation,
		messages,
	)
}
