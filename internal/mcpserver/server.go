package mcpserver

import (
	"context"
	"fmt"
	"log/slog"

	"english-learning-mcp/internal/dictionary"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/learning"
	"english-learning-mcp/internal/vocabulary"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const Version = "3.1.0"

type Services struct {
	Dictionary *dictionary.Service
	Vocabulary *vocabulary.Service
	Learning   *learning.Service
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
		Title:       "English Vocabulary",
		Description: "Look up English words and expressions, save the meanings you want to learn, and build lasting vocabulary with personalized spaced-repetition reviews and contextual practice.",
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
	if err := registerLearningNext(server, services.Learning, logger); err != nil {
		return nil, err
	}
	if err := registerLearningReview(server, services.Learning, logger); err != nil {
		return nil, err
	}
	if err := registerLearningReviewUpdate(server, services.Learning, logger); err != nil {
		return nil, err
	}
	if err := registerReinforcementNext(server, services.Learning, logger); err != nil {
		return nil, err
	}
	if err := registerReinforcementReview(server, services.Learning, logger); err != nil {
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
		Description: "Save one learnable meaning. Pass the exact definition from dictionary_lookup to learn homonyms and polysemous terms separately; all meanings share the cached lookup. General usefulness combines offline word frequencies, idiom/collocation evidence, and an optional double-weighted hint. Conservative expression-variant and typo matching affects usefulness only, never the saved term.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			IdempotentHint:  true,
			OpenWorldHint:   &closedWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularySaveInput) (vocabulary.SaveResult, error) {
		return service.Save(ctx, input.Term, vocabulary.InitialValues{
			Status:            input.Status,
			Usefulness:        input.Usefulness,
			PersonalInterest:  input.PersonalInterest,
			Tags:              input.Tags,
			CustomDescription: input.CustomDescription,
			DescriptionSource: input.DescriptionSource,
			Notes:             input.Notes,
			Examples:          input.Examples,
			Context:           input.Context,
			Definition:        input.Definition,
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
	outputSchema, err := inferredSchema[domain.VocabularyItem]()
	if err != nil {
		return err
	}
	closedWorld := false
	destructive := true
	return registerTool(server, &mcp.Tool{
		Name:        "vocabulary_update",
		Title:       "Update saved vocabulary",
		Description: "Partially update an item's status, personalInterest, usefulness hint, context, tags, description source, notes, or examples. Set personalInterest high for interesting/learn sooner, low for less important without exclusion, or normal to reset. Personal interest is independent of general usefulness; do not change usefulness to express preferences. A usefulness hint is combined with offline evidence, not a forced override. Omitted fields are preserved.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			OpenWorldHint:   &closedWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularyUpdateInput) (domain.VocabularyItem, error) {
		item, err := service.Update(ctx, input.ItemID, input.Term, vocabulary.UpdateChanges{
			Status:            input.Changes.Status,
			Usefulness:        input.Changes.Usefulness,
			PersonalInterest:  input.Changes.PersonalInterest,
			Context:           input.Changes.Context,
			Tags:              input.Changes.Tags,
			CustomDescription: input.Changes.CustomDescription,
			DescriptionSource: input.Changes.DescriptionSource,
			Notes:             input.Changes.Notes,
			Examples:          input.Changes.Examples,
		})
		return item, err
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
	outputSchema, err := inferredSchema[domain.VocabularyItem]()
	if err != nil {
		return err
	}
	closedWorld := false
	return registerTool(server, &mcp.Tool{
		Name:        "vocabulary_get",
		Title:       "Get saved vocabulary",
		Description: "Retrieve one saved meaning with its complete linked dictionary lookup. Use itemId when a term has multiple saved meanings.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularyGetInput) (domain.VocabularyItem, error) {
		return service.Get(ctx, input.ItemID, input.Term)
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

func registerLearningNext(server *mcp.Server, service *learning.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[LearningNextInput]()
	if err != nil {
		return err
	}
	configureInputSchema(inputSchema)
	outputSchema, err := inferredSchema[learning.NextResult]()
	if err != nil {
		return err
	}
	closedWorld := false
	destructive := false
	return registerTool(server, &mcp.Tool{
		Name:        "learning_next",
		Title:       "Get the next vocabulary item",
		Description: "Issue and record one active vocabulary presentation for production recall. After an adaptive last-three-events/30-minute cooldown, selectable due Learning/Relearning steps take priority. Otherwise, new and mature-review shares adapt to their unseen/neglected exposure mass, bounded to 20%-80% for either pool; never-presented cards receive the strongest exposure priority, and presented cards gain priority for up to 30 days. Usefulness weights only new cards; personalInterest weights all selectable new/due pools without excluding low-interest items. With no new or due cards, issue the least recently presented nonrecent future card, or least recently presented overall if all are recent; due time breaks exposure ties. On reason early, stop without an answer or review unless the learner explicitly wants early practice. NOT_FOUND means no active cards; there is no daily/session quota. Every call records a fresh presentation and retries may select a different item. Reissuing a pending token is not another scheduled review. If the returned context does not identify a meaning, ask for clarification instead of guessing. Pass the unchanged reviewToken to learning_review after an answer. For explicit learned-word deep practice without changing FSRS, use reinforcement_next instead.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: &destructive,
			IdempotentHint:  false,
			OpenWorldHint:   &closedWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input LearningNextInput) (learning.NextResult, error) {
		return service.Next(ctx, input.IncludeComments)
	})
}

func registerLearningReview(server *mcp.Server, service *learning.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[LearningReviewInput]()
	if err != nil {
		return err
	}
	configureInputSchema(inputSchema)
	outputSchema, err := inferredSchema[learning.RecordResult]()
	if err != nil {
		return err
	}
	closedWorld := false
	destructive := true
	return registerTool(server, &mcp.Tool{
		Name:        "learning_review",
		Title:       "Record a vocabulary review",
		Description: "Record one production-recall rating with an optional problem comment and use FSRS to schedule the next review. Grade answer quality: again for failed or revealed recall, hard for substantial effort or material hints, good for correct recall without material hints, easy for clearly effortless recall. The submitted grade is used unchanged; answer latency never promotes or penalizes a grade. Returns effectiveRating. Retry with the same rating and comment; reviewToken makes identical retries idempotent and changed payloads are rejected. To correct the latest accepted learning_review, use learning_review_update with its original reviewToken instead of recording a second review.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			IdempotentHint:  true,
			OpenWorldHint:   &closedWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input LearningReviewInput) (learning.RecordResult, error) {
		return service.Record(ctx, learning.RecordOptions{
			ReviewToken: input.ReviewToken,
			Rating:      input.Rating,
			Comment:     input.Comment,
		})
	})
}

func registerLearningReviewUpdate(server *mcp.Server, service *learning.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[LearningReviewUpdateInput]()
	if err != nil {
		return err
	}
	configureInputSchema(inputSchema)
	outputSchema, err := inferredSchema[learning.RecordResult]()
	if err != nil {
		return err
	}
	closedWorld := false
	destructive := true
	return registerTool(server, &mcp.Tool{
		Name:        "learning_review_update",
		Title:       "Correct the latest vocabulary review",
		Description: "Correct only this owner's latest accepted learning_review across all vocabulary items, using that review's original reviewToken. Recalculate FSRS from its original pre-review state at its original review time; this replaces the rating and schedule, never adds a second review or repetition. Omit comment to preserve it, or provide an empty string to clear it. The next card's pending token remains valid. Multiple corrections are allowed while this is still the latest accepted review; an identical correction returns duplicate true, but older reviews are rejected even on retries. Missing, deleted, or archived vocabulary cannot be corrected. This tool never changes reinforcement practice; do not use reinforcement tokens.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			IdempotentHint:  true,
			OpenWorldHint:   &closedWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input LearningReviewUpdateInput) (learning.RecordResult, error) {
		return service.UpdateLatest(ctx, learning.UpdateOptions{
			ReviewToken: input.ReviewToken,
			Rating:      input.Rating,
			Comment:     input.Comment,
		})
	})
}

func registerReinforcementNext(server *mcp.Server, service *learning.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[ReinforcementNextInput]()
	if err != nil {
		return err
	}
	outputSchema, err := inferredSchema[learning.ReinforcementNextResult]()
	if err != nil {
		return err
	}
	closedWorld, destructive := false, false
	return registerTool(server, &mcp.Tool{
		Name:        "reinforcement_next",
		Title:       "Practice a learned word deeply",
		Description: "Select one learned meaning for schedule-independent production practice. A hard six-hour cooldown starts at issuance and covers all saved meanings of the normalized word, even without feedback. Favors useful, personally interesting words, unresolved difficulty, and soft recency among words outside cooldown; comment priority fades with successful practice. Each word has at most 25% chance; requires four distinct learned words outside cooldown. INVALID_ARGUMENT means too few eligible words: pause until cooldowns expire or more learned words are available, never reroll or relax the cap. NOT_FOUND means no learned vocabulary. Returns private tutor context including target, meaning, dated comments, practice state, eligibleWordCount after cooldown, and word selectionProbability. Hide the target and revealing examples; ask for a sentence from a description, targeting past mistakes. If the returned context does not identify a meaning, ask for clarification instead of guessing. Do not fetch ahead. Each successful call records a new presentation/token without changing status or FSRS. Submit that token only to reinforcement_review after an attempt.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: false, OpenWorldHint: &closedWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, _ ReinforcementNextInput) (learning.ReinforcementNextResult, error) {
		return service.ReinforcementNext(ctx)
	})
}

func registerReinforcementReview(server *mcp.Server, service *learning.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[ReinforcementReviewInput]()
	if err != nil {
		return err
	}
	configureInputSchema(inputSchema)
	outputSchema, err := inferredSchema[learning.ReinforcementReviewResult]()
	if err != nil {
		return err
	}
	closedWorld, destructive := false, true
	return registerTool(server, &mcp.Tool{
		Name:        "reinforcement_review",
		Title:       "Record learned-word practice feedback",
		Description: "Record the first genuine production attempt from reinforcement_next, with an optional factual comment about usage, confusion, hints, or recovery. Updates independent reinforcement difficulty and review history, never FSRS, learning status, usefulness, or personalInterest. Again increases difficulty by 1, hard by 0.5; good reduces it by 0.5, easy by 1, bounded 0–4. Answer latency does not change the grade. Guided success never erases initial failure. Identical token/rating/comment retries return the original result with duplicate true; changed payloads are rejected. Never send a learning_next token here.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &closedWorld,
		},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input ReinforcementReviewInput) (learning.ReinforcementReviewResult, error) {
		return service.ReinforcementReview(ctx, learning.RecordOptions{
			ReviewToken: input.ReviewToken, Rating: input.Rating, Comment: input.Comment,
		})
	})
}

func requireServices(services Services) error {
	if services.Dictionary == nil || services.Vocabulary == nil || services.Learning == nil {
		return fmt.Errorf("dictionary, vocabulary, and learning services are required")
	}
	return nil
}
