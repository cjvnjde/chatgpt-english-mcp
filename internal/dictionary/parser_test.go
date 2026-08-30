package dictionary

import (
	"net/url"
	"testing"
)

func TestParseCambridgeHTMLReturnsSourceBackedFields(t *testing.T) {
	t.Parallel()

	baseURL, err := url.Parse("https://dictionary.cambridge.org")
	if err != nil {
		t.Fatal(err)
	}
	html := `
		<html><body>
			<div class="entry-body__el">
				<div class="pos-header">
					<span class="hw dhw">bank</span>
					<span class="pos dpos">noun</span>
					<span class="gram dgram">countable</span>
				</div>
				<div class="uk"><span class="ipa dipa">bæŋk</span><audio><source type="audio/mpeg" src="/media/uk.mp3"></audio></div>
				<div class="us"><span class="ipa dipa">bæŋk</span></div>
				<div class="irreg-infls dinfls"><span class="inf-group dinfg"><span class="lab dlab">plural</span><span class="inf dinf">banks</span></span></div>
				<div class="pr dsense">
					<span class="guideword dsense_gw">(RIVER)</span>
					<div class="sense-body dsense_b">
						<div class="def-block ddef_block">
							<div class="def-info"><span class="epp-xref">B1</span></div>
							<div class="def ddef_d db">sloping land beside a river</div>
							<div class="def-body ddef_b">
								<div class="examp dexamp"><span class="lu dlu">river bank</span><span class="eg deg">We sat on the bank.</span></div>
								<div class="xref synonym"><span class="item"><a><span class="x-h">shore</span></a></span></div>
							</div>
							<div class="dimg"><img src="/images/bank-thumb.jpg" on="tap:viewer.open(src: '/images/bank.jpg')" title="A river bank" alt="grass beside water"><span class="dimg_c">Cambridge</span></div>
						</div>
					</div>
				</div>
				<div class="xref idiom"><a><span class="x-h">break the bank</span></a></div>
			</div>
			<div class="didyoumean"><a href="/spellcheck/english/?q=banks">banks</a></div>
			<div class="dataset" data-id="combinations"><div class="cpexamps-body"><div class="lbb lb-cm"><a class="hdib tb">bank account</a><span class="text dtext">I opened a bank account.</span></div></div></div>
		</body></html>`

	lookup, err := parseCambridgeHTML(html, "bank", "https://dictionary.cambridge.org/dictionary/english/bank", 200, baseURL, 20)
	if err != nil {
		t.Fatalf("parseCambridgeHTML() error = %v", err)
	}
	if len(lookup.Entries) != 1 {
		t.Fatalf("entries length = %d, want 1", len(lookup.Entries))
	}
	entry := lookup.Entries[0]
	if entry.Headword != "bank" || entry.PartOfSpeech != "noun" {
		t.Fatalf("entry = %#v", entry)
	}
	if entry.Audio == nil || entry.Audio.UK == nil || entry.Audio.UK.AudioURL != "https://dictionary.cambridge.org/media/uk.mp3" {
		t.Fatalf("UK audio = %#v", entry.Audio)
	}
	if len(entry.Definitions) != 1 {
		t.Fatalf("definitions length = %d, want 1", len(entry.Definitions))
	}
	definition := entry.Definitions[0]
	if definition.Definition != "sloping land beside a river" || definition.Guideword != "RIVER" {
		t.Fatalf("definition = %#v", definition)
	}
	if len(definition.Examples) != 1 || definition.Examples[0] != "We sat on the bank." {
		t.Fatalf("examples = %#v", definition.Examples)
	}
	if len(definition.Labels) != 2 || definition.Labels[1] != "B1" {
		t.Fatalf("labels = %#v", definition.Labels)
	}
	if len(definition.Synonyms) != 1 || definition.Synonyms[0].Words[0] != "shore" {
		t.Fatalf("synonyms = %#v", definition.Synonyms)
	}
	if len(definition.Images) != 1 || definition.Images[0].ImageURL != "https://dictionary.cambridge.org/images/bank.jpg" {
		t.Fatalf("images = %#v", definition.Images)
	}
	if len(lookup.Suggestions) != 1 || lookup.Suggestions[0] != "banks" {
		t.Fatalf("suggestions = %#v", lookup.Suggestions)
	}
	if len(lookup.Idioms) != 1 || lookup.Idioms[0] != "break the bank" {
		t.Fatalf("idioms = %#v", lookup.Idioms)
	}
	if len(lookup.Collocations) != 1 || lookup.Collocations[0].Phrase != "bank account" {
		t.Fatalf("collocations = %#v", lookup.Collocations)
	}
}
