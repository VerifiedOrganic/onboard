package providers

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

var langTagsOverrides = map[string]string{
	"rust":       RustTagsQuery,
	"javascript": JSTagsQuery,
	"typescript": TSTagsQuery,
	"tsx":        TSXTagsQuery,
}

// BuildTagger constructs a Tagger for a language, or nil if it has no tags query
// or fails to load. Failures are swallowed so one bad grammar can't abort indexing.
func BuildTagger(entry *grammars.LangEntry) (tg *ts.Tagger) {
	defer func() {
		if recover() != nil {
			tg = nil
		}
	}()
	lang := entry.Language()
	if lang == nil {
		return nil
	}
	if q, ok := langTagsOverrides[entry.Name]; ok {
		if t, err := ts.NewTagger(lang, q); err == nil {
			return t
		}
	}
	tagsQuery := grammars.ResolveTagsQuery(*entry)
	if strings.TrimSpace(tagsQuery) == "" {
		return nil
	}
	t, err := ts.NewTagger(lang, tagsQuery)
	if err != nil {
		return nil
	}
	return t
}

// SafeTag runs the tagger, swallowing input-dependent panics from grammars.
func SafeTag(tg *ts.Tagger, src []byte) (tags []ts.Tag) {
	defer func() {
		if recover() != nil {
			tags = nil
		}
	}()
	return tg.Tag(src)
}
