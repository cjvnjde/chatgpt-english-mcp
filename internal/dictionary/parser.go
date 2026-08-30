package dictionary

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"english-learning-mcp/internal/domain"
	"github.com/PuerkitoBio/goquery"
)

var imageSourcePattern = regexp.MustCompile(`src:\s*'([^']+)'`)

func parseCambridgeHTML(
	html string,
	term string,
	sourceURL string,
	status int,
	baseURL *url.URL,
	maxDefinitions int,
) (domain.DictionarySnapshotData, error) {
	data := emptyDictionaryData(sourceURL, status)
	if html == "" {
		return data, nil
	}

	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return domain.DictionarySnapshotData{}, fmt.Errorf("read HTML: %w", err)
	}
	document.Find("script, style, noscript").Remove()

	containers := document.Find(".entry-body__el")
	if containers.Length() == 0 {
		containers = document.Find(".entry")
	}
	containers.EachWithBreak(func(_ int, container *goquery.Selection) bool {
		if len(data.Entries) >= 6 {
			return false
		}

		headword := clean(container.Find(".hw.dhw").First().Text())
		if headword == "" {
			headword = term
		}
		partOfSpeech := clean(container.Find(".pos.dpos").First().Text())
		entry := domain.DictionaryEntry{
			Headword:     headword,
			PartOfSpeech: partOfSpeech,
			Pronunciations: domain.DictionaryPronunciations{
				UK: clean(container.Find(".uk .ipa.dipa, .uk .ipa").First().Text()),
				US: clean(container.Find(".us .ipa.dipa, .us .ipa").First().Text()),
			},
			Inflections: extractInflections(container),
			Definitions: parseDefinitions(container, extractEntryLabels(container), baseURL, maxDefinitions),
			Idioms:      extractIdioms(container),
		}
		if len(entry.Definitions) == 0 {
			return true
		}

		ukAudio := extractAudio(container, "uk", baseURL)
		usAudio := extractAudio(container, "us", baseURL)
		if ukAudio != nil || usAudio != nil {
			entry.Audio = &domain.DictionaryAudioRegions{US: usAudio, UK: ukAudio}
		}
		data.Entries = append(data.Entries, entry)
		return true
	})

	if len(data.Entries) == 0 {
		definitions := parseDefinitions(document.Selection, nil, baseURL, maxDefinitions)
		if len(definitions) > 0 {
			data.Entries = append(data.Entries, domain.DictionaryEntry{
				Headword:    term,
				Definitions: definitions,
			})
		}
	}

	data.Suggestions = extractSuggestions(document.Selection, baseURL)
	data.Images = extractImagesFromScope(document.Selection, baseURL)
	data.Idioms = extractIdioms(document.Selection)
	data.Collocations = extractCollocations(document.Selection)
	return data, nil
}

func parseCambridgeSuggestions(html string, baseURL *url.URL) []string {
	if html == "" {
		return []string{}
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return []string{}
	}
	document.Find("script, style, noscript").Remove()
	return extractSuggestions(document.Selection, baseURL)
}

func parseDefinitions(
	scope *goquery.Selection,
	inheritedLabels []string,
	baseURL *url.URL,
	maxDefinitions int,
) []domain.DictionaryDefinition {
	definitions := make([]domain.DictionaryDefinition, 0)
	seen := make(map[string]struct{})

	scope.Find(".def-block.ddef_block").EachWithBreak(func(_ int, block *goquery.Selection) bool {
		if len(definitions) >= maxDefinitions {
			return false
		}
		definitionText := clean(block.Find(".def.ddef_d.db, .def.ddef_d").First().Text())
		key := strings.ToLower(definitionText)
		if definitionText == "" {
			return true
		}
		if _, ok := seen[key]; ok {
			return true
		}
		seen[key] = struct{}{}

		guideword := clean(block.Closest(".pr.dsense, .sense-block").Find(".guideword.dsense_gw, .guideword").First().Text())
		guideword = strings.Trim(guideword, "()")
		labels := append([]string{}, inheritedLabels...)
		block.Find(".def-info > .lab.dlab, .def-info > .usage.dusage, .def-info > .region.dregion, .def-info > .gram.dgram, .def-info > .register.dreg, .def-info > .domain.ddomain, .def-info > .epp-xref").Each(func(_ int, item *goquery.Selection) {
			labels = append(labels, clean(item.Text()))
		})
		labels = compactStrings(labels, 6)

		usages := extractUsages(block)
		examples := make([]string, 0, len(usages))
		phrases := make([]string, 0, len(usages))
		for _, usage := range usages {
			examples = append(examples, usage.Example)
			phrases = append(phrases, usage.Phrase)
		}

		seeAlso := make([]string, 0)
		block.Find(".xref.see_also a, .xref.compare a").Each(func(_ int, item *goquery.Selection) {
			seeAlso = append(seeAlso, clean(item.Text()))
		})
		phraseTitle := clean(block.Closest(".phrase-block.dphrase-block").Find(".phrase-head .phrase-title").First().Text())
		definition := domain.DictionaryDefinition{
			Definition:  definitionText,
			Examples:    compactStrings(examples, 8),
			Phrases:     compactStrings(phrases, 16),
			SeeAlso:     compactStrings(seeAlso, 5),
			Images:      extractImagesFromScope(block, baseURL),
			Guideword:   guideword,
			PhraseTitle: phraseTitle,
			Labels:      labels,
			Usages:      usages,
			Related:     extractRelatedWords(block),
			Synonyms:    extractSynonyms(block),
			Antonyms:    extractAntonyms(block),
		}
		definitions = append(definitions, definition)
		return true
	})

	if len(definitions) > 0 {
		return definitions
	}
	scope.Find(".def.ddef_d.db, .def.ddef_d").EachWithBreak(func(_ int, item *goquery.Selection) bool {
		if len(definitions) >= maxDefinitions {
			return false
		}
		definitionText := clean(item.Text())
		key := strings.ToLower(definitionText)
		if definitionText == "" {
			return true
		}
		if _, ok := seen[key]; ok {
			return true
		}
		seen[key] = struct{}{}
		definitions = append(definitions, domain.DictionaryDefinition{
			Definition: definitionText,
			Examples:   []string{},
			Phrases:    []string{},
			SeeAlso:    []string{},
			Images:     []domain.DictionaryImage{},
			Labels:     []string{},
		})
		return true
	})
	return definitions
}

func extractUsages(block *goquery.Selection) []domain.DictionaryUsage {
	usages := make([]domain.DictionaryUsage, 0)
	seen := make(map[string]struct{})
	add := func(phrase, example string) {
		phrase = clean(phrase)
		example = clean(example)
		if phrase == "" && example == "" {
			return
		}
		key := strings.ToLower(phrase + "\n" + example)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		usages = append(usages, domain.DictionaryUsage{Phrase: phrase, Example: example})
	}

	block.Find(".def-body.ddef_b .examp.dexamp").Each(func(_ int, example *goquery.Selection) {
		add(
			example.Find(".lu.dlu").First().Text(),
			example.Find(".eg.deg").First().Text(),
		)
	})

	senseBody := block.Closest(".sense-body.dsense_b")
	firstDefinition := senseBody.ChildrenFiltered(".def-block.ddef_block").First()
	if len(block.Nodes) > 0 && len(firstDefinition.Nodes) > 0 && block.Nodes[0] == firstDefinition.Nodes[0] {
		senseBody.ChildrenFiltered(".daccord").Not(".smartt").Find("li.eg, .eg.dexamp").Each(func(_ int, example *goquery.Selection) {
			exampleText := clean(example.Find(".eg.deg").First().Text())
			if exampleText == "" {
				exampleText = clean(example.Text())
			}
			add(example.Find(".lu.dlu").First().Text(), exampleText)
		})
	}
	return usages
}

func extractRelatedWords(block *goquery.Selection) []domain.DictionaryWordGroup {
	groups := make([]domain.DictionaryWordGroup, 0)
	block.Closest(".pr.dsense, .sense-block").Find(".smartt.daccord section").EachWithBreak(func(_ int, section *goquery.Selection) bool {
		if len(groups) >= 2 {
			return false
		}
		words := make([]string, 0)
		section.Find(".daccord_lb li a .base").Each(func(_ int, item *goquery.Selection) {
			words = append(words, clean(item.Text()))
		})
		words = compactStrings(words, 24)
		if len(words) > 0 {
			groups = append(groups, domain.DictionaryWordGroup{
				Topic: clean(section.Find(".daccord_lt a").First().Text()),
				Words: words,
			})
		}
		return true
	})
	return groups
}

func extractSynonyms(block *goquery.Selection) []domain.DictionaryWordGroup {
	groups := extractXrefWordGroups(block, ".xref.synonym, .xref.synonyms")
	groups = append(groups, extractThesaurusWordGroups(block)...)
	if len(groups) > 3 {
		groups = groups[:3]
	}
	return groups
}

func extractAntonyms(block *goquery.Selection) []domain.DictionaryWordGroup {
	groups := extractXrefWordGroups(block, ".xref.opposite, .xref.opposites, .xref.antonym, .xref.antonyms")
	if len(groups) > 3 {
		groups = groups[:3]
	}
	return groups
}

func extractXrefWordGroups(block *goquery.Selection, selector string) []domain.DictionaryWordGroup {
	groups := make([]domain.DictionaryWordGroup, 0)
	block.Find(selector).Each(func(_ int, crossReference *goquery.Selection) {
		words := make([]string, 0)
		crossReference.Find(".item").Each(func(_ int, item *goquery.Selection) {
			word := clean(item.Find(".x-h, .base").First().Text())
			if word == "" {
				word = clean(item.Find("a").First().Text())
			}
			words = append(words, word)
		})
		words = uniqueCleanWords(words, 12)
		if len(words) > 0 {
			groups = append(groups, domain.DictionaryWordGroup{Words: words})
		}
	})
	return groups
}

func extractThesaurusWordGroups(block *goquery.Selection) []domain.DictionaryWordGroup {
	groups := make([]domain.DictionaryWordGroup, 0)
	block.Find(".def-body.ddef_b .daccord section").Each(func(_ int, section *goquery.Selection) {
		if !strings.Contains(strings.ToLower(clean(section.Find("header").First().Text())), "thesaurus") {
			return
		}
		words := make([]string, 0)
		section.Find(".daccord_lb li a").Each(func(_ int, item *goquery.Selection) {
			words = append(words, clean(item.Text()))
		})
		words = uniqueCleanWords(words, 12)
		if len(words) > 0 {
			groups = append(groups, domain.DictionaryWordGroup{
				Topic: clean(section.Find(".daccord_lt a").First().Text()),
				Words: words,
			})
		}
	})
	return groups
}

func extractCollocations(scope *goquery.Selection) []domain.DictionaryCollocation {
	collocations := make([]domain.DictionaryCollocation, 0)
	seen := make(map[string]struct{})
	scope.Find(".dataset[data-id='combinations'] .cpexamps-body .lbb.lb-cm, .cpexamps-body .lbb.lb-cm").EachWithBreak(func(_ int, item *goquery.Selection) bool {
		if len(collocations) >= 12 {
			return false
		}
		phrase := clean(item.Find("a.hdib.tb").First().Text())
		key := strings.ToLower(phrase)
		if phrase == "" {
			return true
		}
		if _, ok := seen[key]; ok {
			return true
		}
		seen[key] = struct{}{}
		example := clean(item.Find(".text.dtext").First().Text())
		if example == "" {
			example = clean(item.Find(".dexamp").First().Text())
		}
		collocations = append(collocations, domain.DictionaryCollocation{Phrase: phrase, Example: example})
		return true
	})
	return collocations
}

func extractInflections(scope *goquery.Selection) []string {
	inflections := make([]string, 0)
	scope.Find(".irreg-infls.dinfls .inf-group.dinfg, .irreg-infls .inf-group").Each(func(_ int, item *goquery.Selection) {
		label := clean(item.Find(".lab.dlab").First().Text())
		forms := make([]string, 0)
		item.Find(".inf.dinf, .inf").Each(func(_ int, form *goquery.Selection) {
			forms = append(forms, clean(form.Text()))
		})
		forms = compactStrings(forms, 0)
		if label != "" && len(forms) > 0 {
			inflections = append(inflections, label+" "+strings.Join(forms, ", "))
			return
		}
		inflections = append(inflections, clean(item.Text()))
	})
	return compactStrings(inflections, 4)
}

func extractEntryLabels(scope *goquery.Selection) []string {
	labels := make([]string, 0)
	scope.Find(".pos-header .gram.dgram, .pos-header .usage.dusage, .pos-header .region.dregion, .pos-header .register.dreg, .pos-header .domain.ddomain").Each(func(_ int, item *goquery.Selection) {
		labels = append(labels, clean(item.Text()))
	})
	return compactStrings(labels, 0)
}

func extractAudio(scope *goquery.Selection, region string, baseURL *url.URL) *domain.DictionaryAudio {
	source := scope.Find("." + region + " audio source[type='audio/mpeg']").First()
	if source.Length() == 0 {
		source = scope.Find("." + region + " audio source").First()
	}
	path, ok := source.Attr("src")
	if !ok {
		return nil
	}
	audioURL := absoluteURL(path, baseURL)
	if audioURL == "" {
		return nil
	}
	contentType, ok := source.Attr("type")
	if !ok || contentType == "" {
		contentType = "audio/mpeg"
	}
	return &domain.DictionaryAudio{AudioURL: audioURL, ContentType: contentType}
}

func extractIdioms(scope *goquery.Selection) []string {
	idioms := make([]string, 0)
	scope.Find(".xref.idiom a .x-h, .xref.idiom a .base, .xref.idiom a").Each(func(_ int, item *goquery.Selection) {
		idioms = append(idioms, clean(item.Text()))
	})
	return compactStrings(idioms, 12)
}

func extractSuggestions(scope *goquery.Selection, baseURL *url.URL) []string {
	candidates := make([]string, 0)
	collectSuggestionLinks(scope.Find(".spell-suggestion a, .didyoumean a"), baseURL, &candidates)

	scope.Find("h1, h2").Each(func(_ int, heading *goquery.Selection) {
		if !strings.Contains(strings.ToLower(clean(heading.Text())), "search suggestions") {
			return
		}
		collectSuggestionLinks(heading.Parent().Find("ul.hul-u li a[href*='/search/english/direct/']"), baseURL, &candidates)
	})
	scope.Find("p").Each(func(_ int, paragraph *goquery.Selection) {
		text := strings.ToLower(clean(paragraph.Text()))
		if !strings.Contains(text, "similar spellings") && !strings.Contains(text, "similar pronunciations") {
			return
		}
		collectSuggestionLinks(paragraph.NextAllFiltered("ul").First().Find("li a"), baseURL, &candidates)
	})
	return uniqueCleanWords(candidates, 8)
}

func collectSuggestionLinks(links *goquery.Selection, baseURL *url.URL, candidates *[]string) {
	links.Each(func(_ int, link *goquery.Selection) {
		fallback := clean(link.Text())
		href, ok := link.Attr("href")
		if !ok {
			*candidates = append(*candidates, fallback)
			return
		}
		parsed, err := url.Parse(href)
		if err == nil {
			queryTerm := baseURL.ResolveReference(parsed).Query().Get("q")
			if queryTerm != "" {
				*candidates = append(*candidates, clean(queryTerm))
				return
			}
		}
		*candidates = append(*candidates, fallback)
	})
}

func extractImagesFromScope(scope *goquery.Selection, baseURL *url.URL) []domain.DictionaryImage {
	images := make([]domain.DictionaryImage, 0)
	seen := make(map[string]struct{})
	scope.Find(".dimg").EachWithBreak(func(_ int, container *goquery.Selection) bool {
		if len(images) >= 3 {
			return false
		}
		image := container.Find("amp-img, img").First()
		thumbnail, _ := image.Attr("src")
		if thumbnail == "" {
			thumbnail, _ = image.Attr("data-src")
		}
		path := thumbnail
		if onValue, ok := image.Attr("on"); ok {
			if match := imageSourcePattern.FindStringSubmatch(onValue); len(match) == 2 {
				path = match[1]
			}
		}
		imageURL := absoluteURL(path, baseURL)
		if imageURL == "" {
			return true
		}
		if _, ok := seen[imageURL]; ok {
			return true
		}
		seen[imageURL] = struct{}{}

		item := domain.DictionaryImage{
			ImageURL: imageURL,
			Credit:   clean(container.Find(".dimg_c").First().Text()),
		}
		if thumbnail != "" {
			item.ThumbnailURL = absoluteURL(thumbnail, baseURL)
		}
		item.Title, _ = image.Attr("title")
		item.Title = clean(item.Title)
		item.Alt, _ = image.Attr("alt")
		item.Alt = clean(item.Alt)
		images = append(images, item)
		return true
	})
	return images
}

func absoluteURL(pathOrURL string, baseURL *url.URL) string {
	if pathOrURL == "" {
		return ""
	}
	parsed, err := url.Parse(pathOrURL)
	if err != nil {
		return ""
	}
	return baseURL.ResolveReference(parsed).String()
}

func emptyDictionaryData(sourceURL string, status int) domain.DictionarySnapshotData {
	return domain.DictionarySnapshotData{
		SourceURL:    sourceURL,
		Status:       status,
		Entries:      []domain.DictionaryEntry{},
		Suggestions:  []string{},
		Images:       []domain.DictionaryImage{},
		Idioms:       []string{},
		Collocations: []domain.DictionaryCollocation{},
	}
}

func compactStrings(values []string, limit int) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = clean(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func uniqueCleanWords(values []string, limit int) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = clean(value)
		key := strings.ToLower(value)
		if value == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func clean(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(value, "\u00a0", " ")), " ")
}
