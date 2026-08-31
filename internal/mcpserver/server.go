package mcpserver

import (
	"context"
	"fmt"
	"log/slog"

	"english-learning-mcp/internal/dictionary"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/vocabulary"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const Version = "2.2.0"

type Services struct {
	Dictionary *dictionary.Service
	Vocabulary *vocabulary.Service
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
		Description: "Permanent dictionary lookups and saved vocabulary tools.",
		Version:     Version,
	}, &mcp.ServerOptions{Logger: logger})

	if err := registerDictionaryLookup(server, services.Dictionary, logger); err != nil {
		return nil, err
	}
	if err := registerVocabularySave(server, services.Vocabulary, logger); err != nil {
		return nil, err
	}
	if err := registerVocabularyUpdate(server, services.Vocabulary, logger); err != nil {
		return nil, err
	}
	if err := registerVocabularyGet(server, services.Vocabulary, logger); err != nil {
		return nil, err
	}
	if err := registerVocabularyList(server, services.Vocabulary, logger); err != nil {
		return nil, err
	}
	if err := registerVocabularyDelete(server, services.Vocabulary, logger); err != nil {
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
		Description: "Return a permanently cached Cambridge lookup. Cambridge is contacted only when no entry is cached or refresh is true.",
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
		Description: "Create or ensure a learning-list term with optional initial metadata. Existing learner metadata is never overwritten; use vocabulary_update for changes.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			IdempotentHint:  true,
			OpenWorldHint:   &closedWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularySaveInput) (vocabulary.SaveResult, error) {
		return service.Save(ctx, input.Term, vocabulary.InitialValues{
			Status:            input.Status,
			Tags:              input.Tags,
			CustomDescription: input.CustomDescription,
			DescriptionSource: input.DescriptionSource,
			Notes:             input.Notes,
			Examples:          input.Examples,
		})
	})
}

func registerVocabularyUpdate(server *mcp.Server, service *vocabulary.Service, logger *slog.Logger) error {
	byIDSchema, err := inferredSchema[vocabularyUpdateByIDInput]()
	if err != nil {
		return err
	}
	byTermSchema, err := inferredSchema[vocabularyUpdateByTermInput]()
	if err != nil {
		return err
	}
	configureInputSchema(byIDSchema)
	configureInputSchema(byTermSchema)
	inputSchema := unionSchema(byIDSchema, byTermSchema)
	outputSchema, err := inferredSchema[VocabularyUpdateOutput]()
	if err != nil {
		return err
	}
	closedWorld := false
	destructive := true
	return registerTool(server, &mcp.Tool{
		Name:        "vocabulary_update",
		Title:       "Update saved vocabulary",
		Description: "Partially update an existing item's status, tags, description source, notes, or personal examples. Omitted fields are preserved.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			OpenWorldHint:   &closedWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularyUpdateInput) (VocabularyUpdateOutput, error) {
		item, err := service.Update(ctx, input.ItemID, input.Term, vocabulary.UpdateChanges{
			Status:            input.Changes.Status,
			Tags:              input.Changes.Tags,
			CustomDescription: input.Changes.CustomDescription,
			DescriptionSource: input.Changes.DescriptionSource,
			Notes:             input.Changes.Notes,
			Examples:          input.Changes.Examples,
		})
		return VocabularyUpdateOutput{Item: item}, err
	})
}

func registerVocabularyGet(server *mcp.Server, service *vocabulary.Service, logger *slog.Logger) error {
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
	inputSchema := unionSchema(byIDSchema, byTermSchema)
	outputSchema, err := inferredSchema[VocabularyGetOutput]()
	if err != nil {
		return err
	}
	closedWorld := false
	return registerTool(server, &mcp.Tool{
		Name:        "vocabulary_get",
		Title:       "Get saved vocabulary",
		Description: "Retrieve one saved term together with its complete linked dictionary lookup.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularyGetInput) (VocabularyGetOutput, error) {
		item, err := service.Get(ctx, input.ItemID, input.Term)
		return VocabularyGetOutput{Item: item}, err
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
		Description: "List saved terms with their complete linked dictionary lookups and opaque cursor pagination.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularyListInput) (VocabularyListOutput, error) {
		result, err := service.List(ctx, vocabulary.ListOptions{
			Query:                input.Query,
			Statuses:             input.Statuses,
			Tags:                 input.Tags,
			HasLookup:            input.HasLookup,
			HasCustomDescription: input.HasCustomDescription,
			Sort:                 string(input.Sort),
			Limit:                input.Limit,
			Cursor:               input.Cursor,
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
		Description: "Remove one saved term without deleting its permanent dictionary lookup.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularyDeleteInput) (VocabularyDeleteOutput, error) {
		if err := service.Delete(ctx, input.ItemID); err != nil {
			return VocabularyDeleteOutput{}, err
		}
		return VocabularyDeleteOutput{Deleted: true, ItemID: input.ItemID}, nil
	})
}

func requireServices(services Services) error {
	if services.Dictionary == nil || services.Vocabulary == nil {
		return fmt.Errorf("dictionary and vocabulary services are required")
	}
	return nil
}
