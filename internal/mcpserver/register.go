package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"english-learning-mcp/internal/apperr"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type typedHandler[Input, Output any] func(context.Context, Input) (Output, error)

func registerTool[Input, Output any](
	server *mcp.Server,
	tool *mcp.Tool,
	inputSchema *jsonschema.Schema,
	outputSchema *jsonschema.Schema,
	logger *slog.Logger,
	handler typedHandler[Input, Output],
) error {
	inputResolved, err := inputSchema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("resolve %s input schema: %w", tool.Name, err)
	}
	outputResolved, err := outputSchema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("resolve %s output schema: %w", tool.Name, err)
	}
	tool.InputSchema = inputSchema
	tool.OutputSchema = outputSchema

	server.AddTool(tool, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var raw json.RawMessage
		if request.Params.Arguments != nil {
			raw = request.Params.Arguments
		} else {
			raw = json.RawMessage("{}")
		}

		var instance any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if err := decoder.Decode(&instance); err != nil {
			return toolErrorResult(logger, tool.Name, apperr.New(apperr.InvalidArgument, safeDecodeMessage(err))), nil
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return toolErrorResult(logger, tool.Name, apperr.New(apperr.InvalidArgument, "arguments must contain one JSON object")), nil
		}
		if err := inputResolved.ApplyDefaults(&instance); err != nil {
			return toolErrorResult(logger, tool.Name, apperr.Wrap(apperr.InternalError, "failed to apply tool defaults", err)), nil
		}
		if err := inputResolved.Validate(instance); err != nil {
			return toolErrorResult(logger, tool.Name, apperr.New(
				apperr.InvalidArgument,
				"arguments do not match the declared tool schema; inspect required fields and allowed values",
			)), nil
		}
		defaulted, err := json.Marshal(instance)
		if err != nil {
			return toolErrorResult(logger, tool.Name, apperr.Wrap(apperr.InternalError, "failed to prepare tool arguments", err)), nil
		}

		var input Input
		if err := json.Unmarshal(defaulted, &input); err != nil {
			return toolErrorResult(logger, tool.Name, apperr.New(apperr.InvalidArgument, safeDecodeMessage(err))), nil
		}
		output, err := handler(ctx, input)
		if err != nil {
			return toolErrorResult(logger, tool.Name, err), nil
		}

		encoded, err := json.Marshal(output)
		if err != nil {
			return toolErrorResult(logger, tool.Name, apperr.Wrap(apperr.InternalError, "failed to encode the tool result", err)), nil
		}
		var outputInstance any
		outputDecoder := json.NewDecoder(bytes.NewReader(encoded))
		// Image metadata is re-encoded below; preserve exact int64 values.
		outputDecoder.UseNumber()
		if err := outputDecoder.Decode(&outputInstance); err != nil {
			return toolErrorResult(logger, tool.Name, apperr.Wrap(apperr.InternalError, "failed to validate the tool result", err)), nil
		}
		images, changed := embedDictionaryImages(ctx, outputInstance)
		if changed {
			encoded, err = json.Marshal(outputInstance)
			if err != nil {
				return toolErrorResult(logger, tool.Name, apperr.Wrap(apperr.InternalError, "failed to encode image metadata", err)), nil
			}
		}
		// The schema validator expects ordinary JSON numbers, while the wire
		// representation above retains full integer precision.
		var validationInstance any
		if err := json.Unmarshal(encoded, &validationInstance); err != nil {
			return toolErrorResult(logger, tool.Name, apperr.Wrap(apperr.InternalError, "failed to validate image metadata", err)), nil
		}
		if err := outputResolved.Validate(validationInstance); err != nil {
			return toolErrorResult(logger, tool.Name, apperr.Wrap(apperr.InternalError, "tool result did not match its declared schema", err)), nil
		}

		return &mcp.CallToolResult{
			Content:           append([]mcp.Content{&mcp.TextContent{Text: string(encoded)}}, images...),
			StructuredContent: json.RawMessage(encoded),
		}, nil
	})
	return nil
}

func toolErrorResult(logger *slog.Logger, toolName string, err error) *mcp.CallToolResult {
	applicationError := apperr.From(err)
	if applicationError.Code == apperr.InternalError {
		logger.Error("MCP tool failed", "tool", toolName, "code", applicationError.Code)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: applicationError.Error()}},
		IsError: true,
	}
}

func safeDecodeMessage(err error) string {
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		if typeError.Field != "" {
			return "argument " + typeError.Field + " has an incompatible JSON type"
		}
		return "an argument has an incompatible JSON type"
	}
	message := err.Error()
	if strings.HasPrefix(message, "json: unknown field ") {
		return "arguments contain " + strings.TrimPrefix(message, "json: ")
	}
	return "arguments must be a valid JSON object"
}
