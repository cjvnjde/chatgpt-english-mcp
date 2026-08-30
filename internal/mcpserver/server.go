package mcpserver

import (
	"context"
	"fmt"
	"log/slog"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/dictionary"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/explanation"
	"english-learning-mcp/internal/vocabulary"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const Version = "1.0.0"

type Services struct {
	Dictionary  *dictionary.Service
	Vocabulary  *vocabulary.Service
	Explanation *explanation.Service
}

func New(services Services, logger *slog.Logger) (*mcp.Server, error) {
	if err := requireServices(services); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:        "english-learning-mcp",
		Title:       "English Learning",
		Description: "Source-backed dictionary, learner explanation cache, and saved vocabulary tools.",
		Version:     Version,
	}, nil)

	if err := registerDictionaryLookup(server, services.Dictionary, logger); err != nil {
		return nil, err
	}
	if err := registerVocabularySave(server, services.Vocabulary, logger); err != nil {
		return nil, err
	}
	if err := registerVocabularyGet(server, services.Vocabulary, services.Explanation, logger); err != nil {
		return nil, err
	}
	if err := registerVocabularyList(server, services.Vocabulary, logger); err != nil {
		return nil, err
	}
	if err := registerVocabularyDelete(server, services.Vocabulary, logger); err != nil {
		return nil, err
	}
	if err := registerExplanationGet(server, services.Explanation, logger); err != nil {
		return nil, err
	}
	if err := registerExplanationsList(server, services.Explanation, logger); err != nil {
		return nil, err
	}
	if err := registerExplanationWrite(server, services.Explanation, logger); err != nil {
		return nil, err
	}
	return server, nil
}

func registerDictionaryLookup(server *mcp.Server, service *dictionary.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[DictionaryLookupInput]()
	if err != nil {
		return err
	}
	configureInputSchema(inputSchema)
	setDefault(inputSchema, "refresh", "false")
	outputSchema, err := inferredSchema[domain.DictionaryLookupResult]()
	if err != nil {
		return err
	}
	openWorld := true
	destructive := false
	return registerTool(server, &mcp.Tool{
		Name:        "dictionary_lookup",
		Title:       "Look up dictionary facts",
		Description: "Fetch Cambridge source facts through an immutable read-through cache. This performs no AI work and never saves the term to vocabulary.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			OpenWorldHint:   &openWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input DictionaryLookupInput) (domain.DictionaryLookupResult, error) {
		return service.Lookup(ctx, input.Term, input.Refresh)
	})
}

func registerVocabularySave(server *mcp.Server, service *vocabulary.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[VocabularySaveInput]()
	if err != nil {
		return err
	}
	configureInputSchema(inputSchema)
	outputSchema, err := inferredSchema[vocabulary.SaveResult]()
	if err != nil {
		return err
	}
	closedWorld := false
	destructive := false
	return registerTool(server, &mcp.Tool{
		Name:        "vocabulary_save",
		Title:       "Save vocabulary",
		Description: "Idempotently add one term to the authenticated learner's vocabulary collection. It does not perform a dictionary lookup or create an explanation.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			IdempotentHint:  true,
			OpenWorldHint:   &closedWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularySaveInput) (vocabulary.SaveResult, error) {
		return service.Save(ctx, input.Term)
	})
}

func registerVocabularyGet(
	server *mcp.Server,
	vocabularyService *vocabulary.Service,
	explanationService *explanation.Service,
	logger *slog.Logger,
) error {
	byIDSchema, err := inferredSchema[vocabularyGetByIDInput]()
	if err != nil {
		return err
	}
	byTermSchema, err := inferredSchema[vocabularyGetByTermInput]()
	if err != nil {
		return err
	}
	configureInputSchema(byIDSchema)
	configureInputSchema(byTermSchema)
	setDefault(byIDSchema, "includeStaleExplanations", "false")
	setDefault(byTermSchema, "includeStaleExplanations", "false")
	inputSchema := unionSchema(byIDSchema, byTermSchema)
	outputSchema, err := inferredSchema[VocabularyGetOutput]()
	if err != nil {
		return err
	}
	closedWorld := false
	return registerTool(server, &mcp.Tool{
		Name:        "vocabulary_get",
		Title:       "Get saved vocabulary",
		Description: "Retrieve exactly one saved vocabulary item and its explanation summaries. The item must already be saved.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularyGetInput) (VocabularyGetOutput, error) {
		item, err := vocabularyService.Get(ctx, input.ItemID, input.Term)
		if err != nil {
			return VocabularyGetOutput{}, err
		}
		summaries, err := explanationService.SummariesForTerm(ctx, item.NormalizedTerm, input.IncludeStaleExplanations)
		if err != nil {
			return VocabularyGetOutput{}, err
		}
		return VocabularyGetOutput{Item: item, Explanations: summaries}, nil
	})
}

func registerVocabularyList(server *mcp.Server, service *vocabulary.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[VocabularyListInput]()
	if err != nil {
		return err
	}
	configureInputSchema(inputSchema)
	configureListSchema(inputSchema)
	outputSchema, err := inferredSchema[VocabularyListOutput]()
	if err != nil {
		return err
	}
	closedWorld := false
	return registerTool(server, &mcp.Tool{
		Name:        "vocabulary_list",
		Title:       "List saved vocabulary",
		Description: "List and filter saved vocabulary with opaque cursor pagination. Results contain compact item records rather than full explanations.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularyListInput) (VocabularyListOutput, error) {
		result, err := service.List(ctx, vocabulary.ListOptions{
			Query:          input.Query,
			CEFR:           input.CEFR,
			HasExplanation: input.HasExplanation,
			Sort:           string(input.Sort),
			Limit:          input.Limit,
			Cursor:         input.Cursor,
		})
		return VocabularyListOutput{Items: result.Items, NextCursor: result.NextCursor}, err
	})
}

func registerVocabularyDelete(server *mcp.Server, service *vocabulary.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[VocabularyDeleteInput]()
	if err != nil {
		return err
	}
	configureInputSchema(inputSchema)
	outputSchema, err := inferredSchema[VocabularyDeleteOutput]()
	if err != nil {
		return err
	}
	closedWorld := false
	destructive := true
	return registerTool(server, &mcp.Tool{
		Name:        "vocabulary_delete",
		Title:       "Remove saved vocabulary",
		Description: "Remove one item from vocabulary without deleting dictionary snapshots or cached explanations.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularyDeleteInput) (VocabularyDeleteOutput, error) {
		if err := service.Delete(ctx, input.ItemID); err != nil {
			return VocabularyDeleteOutput{}, err
		}
		return VocabularyDeleteOutput{Deleted: true, ItemID: input.ItemID}, nil
	})
}

func registerExplanationGet(server *mcp.Server, service *explanation.Service, logger *slog.Logger) error {
	byIDSchema, err := inferredSchema[explanationGetByIDInput]()
	if err != nil {
		return err
	}
	byKeySchema, err := inferredSchema[explanationGetByKeyInput]()
	if err != nil {
		return err
	}
	configureInputSchema(byIDSchema)
	configureInputSchema(byKeySchema)
	setDefault(byIDSchema, "includeStale", "false")
	setDefault(byKeySchema, "context", `""`)
	setDefault(byKeySchema, "includeStale", "false")
	inputSchema := unionSchema(byIDSchema, byKeySchema)

	foundSchema, err := inferredSchema[explanationFoundOutput]()
	if err != nil {
		return err
	}
	setConst(foundSchema, "found", true)
	missSchema, err := inferredSchema[explanationMissOutput]()
	if err != nil {
		return err
	}
	setConst(missSchema, "found", false)
	missSchema.Properties["reason"].Enum = []any{"not_cached", "stale_only"}
	outputSchema := unionSchema(foundSchema, missSchema)

	closedWorld := false
	return registerTool(server, &mcp.Tool{
		Name:        "explanation_get",
		Title:       "Get cached explanation",
		Description: "Retrieve one full context-specific explanation by ID or exact natural cache key. A normal cache miss is returned as found=false.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input ExplanationGetInput) (ExplanationGetOutput, error) {
		result, err := service.Get(ctx, explanation.GetOptions{
			ExplanationID: input.ExplanationID,
			Term:          input.Term,
			Context:       input.Context,
			Generator:     input.Generator,
			IncludeStale:  input.IncludeStale,
		})
		if err != nil {
			return ExplanationGetOutput{}, err
		}
		if result.Found {
			return ExplanationGetOutput{Found: true, Explanation: result.Explanation}, nil
		}
		normalizedTerm := result.NormalizedTerm
		normalizedContext := result.NormalizedContext
		return ExplanationGetOutput{
			Found:             false,
			NormalizedTerm:    &normalizedTerm,
			NormalizedContext: &normalizedContext,
			Reason:            result.Reason,
		}, nil
	})
}

func registerExplanationsList(server *mcp.Server, service *explanation.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[ExplanationsListInput]()
	if err != nil {
		return err
	}
	configureInputSchema(inputSchema)
	configureListSchema(inputSchema)
	setDefault(inputSchema, "onlySavedItems", "false")
	setDefault(inputSchema, "includeStale", "false")
	outputSchema, err := inferredSchema[ExplanationsListOutput]()
	if err != nil {
		return err
	}
	closedWorld := false
	return registerTool(server, &mcp.Tool{
		Name:        "explanations_list",
		Title:       "List cached explanations",
		Description: "List compact explanation summaries, including unsaved terms unless onlySavedItems is true. Use explanation_get for full content.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input ExplanationsListInput) (ExplanationsListOutput, error) {
		result, err := service.List(ctx, explanation.ListOptions{
			Term:           input.Term,
			ItemID:         input.ItemID,
			CEFR:           input.CEFR,
			OnlySavedItems: input.OnlySavedItems,
			IncludeStale:   input.IncludeStale,
			Sort:           string(input.Sort),
			Limit:          input.Limit,
			Cursor:         input.Cursor,
		})
		return ExplanationsListOutput{Explanations: result.Explanations, NextCursor: result.NextCursor}, err
	})
}

func registerExplanationWrite(server *mcp.Server, service *explanation.Service, logger *slog.Logger) error {
	upsertInputSchema, err := inferredSchema[explanationUpsertInput]()
	if err != nil {
		return err
	}
	deleteInputSchema, err := inferredSchema[explanationDeleteInput]()
	if err != nil {
		return err
	}
	configureInputSchema(upsertInputSchema)
	configureInputSchema(deleteInputSchema)
	setConst(upsertInputSchema, "op", "upsert")
	setConst(deleteInputSchema, "op", "delete")
	inputSchema := unionSchema(upsertInputSchema, deleteInputSchema)

	upsertOutputSchema, err := inferredSchema[explanationUpsertOutput]()
	if err != nil {
		return err
	}
	deleteOutputSchema, err := inferredSchema[explanationDeleteOutput]()
	if err != nil {
		return err
	}
	setConst(deleteOutputSchema, "deleted", true)
	outputSchema := unionSchema(upsertOutputSchema, deleteOutputSchema)

	closedWorld := false
	destructive := true
	return registerTool(server, &mcp.Tool{
		Name:        "explanation_write",
		Title:       "Write cached explanation",
		Description: "Explicitly upsert or delete an owner-scoped learner explanation. Upserts reference immutable source indexes and never save vocabulary implicitly.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input ExplanationWriteInput) (ExplanationWriteOutput, error) {
		switch input.Op {
		case ExplanationUpsert:
			if input.Value == nil {
				return ExplanationWriteOutput{}, apperr.New(apperr.InvalidArgument, "value is required for op=upsert")
			}
			result, err := service.Upsert(ctx, *input.Value)
			if err != nil {
				return ExplanationWriteOutput{}, err
			}
			created := result.Created
			return ExplanationWriteOutput{Created: &created, Explanation: &result.Explanation}, nil
		case ExplanationDelete:
			if err := service.Delete(ctx, input.ExplanationID); err != nil {
				return ExplanationWriteOutput{}, err
			}
			return ExplanationWriteOutput{Deleted: true, ExplanationID: input.ExplanationID}, nil
		default:
			return ExplanationWriteOutput{}, apperr.New(apperr.InvalidArgument, "op must be upsert or delete")
		}
	})
}

func requireServices(services Services) error {
	if services.Dictionary == nil || services.Vocabulary == nil || services.Explanation == nil {
		return fmt.Errorf("all MCP services are required")
	}
	return nil
}
