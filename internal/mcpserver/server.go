package mcpserver

import (
	"context"
	"fmt"
	"log/slog"

	"english-learning-mcp/internal/dictionary"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/learning"
	"english-learning-mcp/internal/storage"
	"english-learning-mcp/internal/vocabulary"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const Version = "3.1.0"

type Services struct {
	Dictionary *dictionary.Service
	Vocabulary *vocabulary.Service
	Learning   *learning.Service
	Media      MediaReader
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

	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if services.Media != nil && method == "tools/call" {
				ctx = context.WithValue(ctx, imageMediaReaderKey{}, services.Media)
			}
			return next(ctx, method, request)
		}
	})

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
	if err := registerLearningFocus(server, services.Learning, logger); err != nil {
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
		Description: "List saved vocabulary with complete linked dictionary lookups, or select words and phrases for free-form sentence-writing, conversation, and word-list exercises. This read-only tool creates no review token or presentation, requires no feedback submission, and does not start, consume, record, or change learning or reinforcement reviews or state. Different filters combine with AND; statuses and partsOfSpeech match any supplied value, while tags must all match. Each saved sense remains a separate item. Use statuses learning and learned for exercises; omitted statuses include archived items. Sort random returns a fresh sample from the full filtered result without pagination or a cursor; use excludeItemIds to avoid session repetition. Other sorts use opaque cursor pagination.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input VocabularyListInput) (VocabularyListOutput, error) {
		result, err := service.List(ctx, vocabulary.ListOptions{
			Query:                input.Query,
			Statuses:             input.Statuses,
			Tags:                 input.Tags,
			TermType:             string(input.TermType),
			PartsOfSpeech:        input.PartsOfSpeech,
			Usefulness:           input.Usefulness,
			PersonalInterest:     input.PersonalInterest,
			ExcludeItemIDs:       input.ExcludeItemIDs,
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
	configureLearningNextOutput(outputSchema)
	closedWorld := false
	destructive := false
	return registerTool(server, &mcp.Tool{
		Name:        "learning_next",
		Title:       "Get the next vocabulary item",
		Description: "Follow the persisted learningMode. In focused mode, only unfinished members of the saved batch are eligible; focus reports batch progress. Batch membership survives chats and restarts, and the next batch starts only when every member is learned or explicitly removed. If none is due, return reason waiting with nextDueAt and focus but NO card or reviewToken; pause without recording a review or changing focus. Reason complete means no unfinished vocabulary remains. Use learning_focus to inspect or explicitly start/stop/choose a batch. In mixed mode, or for an eligible focused card, issue and record one active vocabulary presentation for production recall. Returns status and masteryStreak plus available tutoring context directly: customDescription, descriptionSource, notes, all personal examples, tags, and the selected dictionary sense (definition, examples, part of speech, pronunciations), alongside the compact definition and example. No vocabulary_get is needed for these fields. The latest review comment is always included when available; includeComments adds all saved review comments. After an adaptive last-three-events/30-minute cooldown, selectable due Learning/Relearning steps take priority. In mixed mode, otherwise learned words receive a 10%-40% backlog-weighted share when competing with active new/learning words; a sole group receives all selections. The remaining new/nonlearned-review mix follows exposure need, bounded to 20%-80% when both exist. Personal interest weights all pools; usefulness weights FSRS-new cards. In mixed mode learned words remain eligible, but future learned cards do not displace new/due work. In mixed mode with no new or due cards, prefer nonrecent future cards, then nonlearned status, then least recent presentation and earliest due time. On reason early, stop without an answer or review unless the learner explicitly wants early practice. NOT_FOUND means no active cards; there is no daily/session quota. Every issued card records a fresh presentation and retries may select a different item; waiting/complete responses record no presentation. Reissuing a pending token is not another scheduled review and presentation alone never advances mastery. If the returned context does not identify a meaning, ask for clarification instead of guessing. Pass the unchanged reviewToken to learning_review after an answer; status changes automatically from that feedback. For explicit learned-word deep practice, use reinforcement_next instead.",
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

func registerLearningFocus(server *mcp.Server, service *learning.Service, logger *slog.Logger) error {
	inputSchema, err := inferredSchema[LearningFocusInput]()
	if err != nil {
		return err
	}
	inputSchema.Properties["action"] = enumSchema("status", "start", "stop")
	setDefault(inputSchema, "action", `"status"`)
	minimum, maximum, maxLength := 1, 100, 200
	inputSchema.Properties["itemIds"].MinItems = &minimum
	inputSchema.Properties["itemIds"].MaxItems = &maximum
	inputSchema.Properties["itemIds"].UniqueItems = true
	inputSchema.Properties["itemIds"].Items.MinLength = &minimum
	inputSchema.Properties["itemIds"].Items.MaxLength = &maxLength
	outputSchema, err := inferredSchema[storage.LearningFocus]()
	if err != nil {
		return err
	}
	destructive, openWorld := true, false
	return registerTool(server, &mcp.Tool{
		Name: "learning_focus", Title: "Manage a focused learning batch",
		Description: "Inspect or manage the persistent learning batch. action status (default) is read-only and returns the mode, configured batch size, exact saved meanings, mastery and due progress. action start enables focused mode: without itemIds it resumes the unfinished batch or chooses up to focusBatchSize meanings, prioritizing learning status, personal interest, usefulness, then oldest saved. With itemIds it explicitly replaces the batch with those new/learning meanings; use vocabulary_list to find exact meaning IDs first. Batch size (default 10, range 1-100) is configured in Admin Settings; size changes apply to the next batch. action stop returns to mixed selection while retaining the batch for later resumption. Start/stop also update Admin settings and their revision. Never replace a batch unless the learner requests it. Focus applies to learning_next only; ordinary review tokens and mastery rules still apply. No vocabulary statuses, schedules, grades, or presentations are changed by this tool. A finished batch advances automatically on the next learning_next. Learned-word reinforcement remains separate.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: false, OpenWorldHint: &openWorld},
	}, inputSchema, outputSchema, logger, func(ctx context.Context, input LearningFocusInput) (storage.LearningFocus, error) {
		return service.Focus(ctx, input.Action, input.ItemIDs)
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
		Description: "Record the first genuine production-recall attempt with an optional problem comment and use FSRS to schedule the next review. Grade answer quality: again for failed, revealed, or materially assisted recall; hard for successful but effortful unaided recall; good for flawless independent recall; easy for clearly effortless precise recall. The server promotes good to easy when received strictly within the configured fast-answer window after the token's first presentation (default 30 seconds); it never penalizes slow answers or changes other grades. Automatically changes new to learning. Promotion to learned requires the configured number of flawless daily sessions and minimum FSRS Review interval (defaults five days and 21 scheduled days). Good/easy adds at most one masteryStreak credit per word per UTC calendar day, including after a reset that day. Every again or hard resets masteryStreak; again also returns learned vocabulary to learning. Hard alone does not demote a learned word. Every genuine attempt still updates FSRS. Returns status, masteryStreak, and the final stored rating. Retry with the original request rating and comment, even after promotion; identical retries replay the saved result and changed payloads are rejected. To correct the latest accepted learning_review, use learning_review_update with its original reviewToken instead of recording a second review.",
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
		Description: "Correct only this owner's latest accepted learning_review across all vocabulary items, using that review's original reviewToken. Use the requested rating unchanged, without fast-answer promotion. Recalculate FSRS, masteryStreak, and automatic status using current algorithm settings from the original pre-review state at the original review time; never adds a second review or repetition. Omit comment to preserve it, or provide an empty string to clear it. The next card's pending token remains valid. Multiple corrections are allowed while this is still the latest accepted review; an identical correction returns duplicate true, but older reviews are rejected even on retries. Changed corrections are also rejected after newer reinforcement feedback for that word, so later recovery state cannot be overwritten. Missing, deleted, or archived vocabulary cannot be corrected. This tool never changes reinforcement practice; do not use reinforcement tokens.",
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
		Description: "Select one learned meaning for schedule-independent production practice. A configurable cooldown (default six hours) starts at issuance and covers all saved meanings of the normalized word, even without feedback. Favors useful, personally interesting words, unresolved difficulty, and soft recency among words outside cooldown; comment priority fades with successful practice. Each word has at most the configured probability cap (default 25%); requires at least four distinct eligible learned words, or more for a lower cap. INVALID_ARGUMENT means too few eligible words: pause until cooldowns expire or more learned words are available, never reroll or relax the cap. NOT_FOUND means no learned vocabulary. Returns private tutor context including target, meaning, dated comments, practice state, eligibleWordCount after cooldown, and word selectionProbability. Hide the target and revealing examples; ask for a sentence from a description, targeting past mistakes. If the returned context does not identify a meaning, ask for clarification instead of guessing. Do not fetch ahead. Each successful call records a new presentation/token without changing status or FSRS. Submit that token only to reinforcement_review after an attempt.",
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
		Description: "Record the first genuine production attempt from reinforcement_next, with an optional factual comment about usage, confusion, hints, or recovery. Again means failed, materially assisted, or incorrect recall; hard means effortful successful unaided recall; good/easy mean flawless independent recall. Updates independent reinforcement difficulty and history: again +1, hard +0.5, good -0.5, easy -1, bounded 0-4. Again also changes the word to learning, resets masteryStreak, and brings its normal review due time forward to now; hard resets the streak but retains learned status. Does not add an FSRS repetition or change memory parameters, usefulness, or personalInterest. Returns resulting status. Answer latency does not change the grade; guided success never erases initial failure. Identical token/rating/comment retries return the original result with duplicate true, even after demotion; changed payloads are rejected. Never send a learning_next token here.",
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
