package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"saras-tutor/db"
	"saras-tutor/llm"
)

// ── Taxonomy-derived maps (built once at init from seed data) ──

var (
	parsingPrompt     string
	validSubjects     map[string]string // normalized → canonical subject name
	validChapters     map[string]string // normalized → canonical chapter name
	validTopics       map[string]string // normalized → canonical topic name
	topicToChapterMap map[string]string // normalized topic → canonical chapter
	chapterToSubject  map[string]string // normalized chapter → canonical subject
)

// normalize converts a name to a lowercase lookup key:
//
//	"Coulomb's law" → "coulombs_law"
func normalize(s string) string {
	r := strings.NewReplacer(
		" ", "_",
		"'", "",
		"\u2019", "", // right single quote
		"-", "_",
		",", "",
		"(", "",
		")", "",
		":", "",
		"/", "_",
		".", "",
	)
	return strings.ToLower(strings.TrimSpace(r.Replace(s)))
}

func init() {
	taxonomy := db.BuildTaxonomy()

	validSubjects = make(map[string]string, 4)
	validChapters = make(map[string]string, 64)
	validTopics = make(map[string]string, 600)
	topicToChapterMap = make(map[string]string, 600)
	chapterToSubject = make(map[string]string, 64)

	var prompt strings.Builder
	prompt.WriteString("VALID SUBJECTS, CHAPTERS AND TOPICS:\n\n")

	for _, subj := range taxonomy {
		validSubjects[normalize(subj.Name)] = subj.Name
		prompt.WriteString(fmt.Sprintf("══ %s ══\n", strings.ToUpper(subj.Name)))

		for _, ch := range subj.Chapters {
			nch := normalize(ch.Name)
			validChapters[nch] = ch.Name
			chapterToSubject[nch] = subj.Name

			topicNames := make([]string, 0, len(ch.Topics))
			for _, tp := range ch.Topics {
				ntp := normalize(tp.Name)
				validTopics[ntp] = tp.Name
				// first chapter wins for ambiguous topic names
				if _, exists := topicToChapterMap[ntp]; !exists {
					topicToChapterMap[ntp] = ch.Name
				}
				topicNames = append(topicNames, tp.Name)
			}
			prompt.WriteString(fmt.Sprintf("  %s: %s\n", ch.Name, strings.Join(topicNames, ", ")))
		}
		prompt.WriteString("\n")
	}

	parsingPrompt = `You are an academic question parser for JEE (Mains + Advanced) and NEET level problems.

TASK: Parse the student's question into a structured JSON format.

` + prompt.String() + `DIFFICULTY LEVELS: 1 (easy), 2 (medium), 3 (hard), 4 (very hard)

INSTRUCTIONS:
1. Identify the SUBJECT: Physics, Chemistry, Mathematics, or Biology.
2. Identify the CHAPTER from the list above.
3. Identify 1-5 specific TOPICS within that chapter from the list above.
4. Estimate difficulty (1-4 scale).
5. Extract any given variables/values (e.g. mass = 2 kg, velocity = 10 m/s).
6. Clean up the question text (remove noise, ensure clarity).

Use the EXACT chapter and topic names from the lists above.
If you cannot parse any part, use null or empty array. Always return valid JSON.

RESPOND WITH ONLY valid JSON (no code fences, no extra text):
{
  "subject": "Physics",
  "chapter": "Kinematics",
  "topics": ["Projectile motion", "Uniform circular motion"],
  "difficulty": 2,
  "question": "Clean, well-formed question text",
  "variables": {"var_name": "value with unit"}
}
`
}

// ParsedQuestion represents the structured output after parsing.
type ParsedQuestion struct {
	Subject    string            `json:"subject"`
	Chapter    string            `json:"chapter"`
	Topics     []string          `json:"topics"`
	Difficulty int               `json:"difficulty"`
	Question   string            `json:"question"`
	Variables  map[string]string `json:"variables"`
}

func canonicalSubject(raw string) string {
	if canonical, ok := validSubjects[normalize(raw)]; ok {
		return canonical
	}
	return ""
}

func canonicalChapter(raw string) string {
	if canonical, ok := validChapters[normalize(raw)]; ok {
		return canonical
	}
	return ""
}

func canonicalTopics(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		n := normalize(item)
		if n == "" || seen[n] {
			continue
		}
		if canonical, ok := validTopics[n]; ok {
			seen[n] = true
			out = append(out, canonical)
		}
	}
	return out
}

func inferChapter(raw string, topics []string) string {
	if c := canonicalChapter(raw); c != "" {
		return c
	}
	for _, t := range topics {
		n := normalize(t)
		if ch, ok := topicToChapterMap[n]; ok {
			return ch
		}
	}
	return ""
}

func inferSubject(raw string, chapter string) string {
	if s := canonicalSubject(raw); s != "" {
		return s
	}
	if chapter != "" {
		if s, ok := chapterToSubject[normalize(chapter)]; ok {
			return s
		}
	}
	return ""
}

func parseDifficulty(raw any) int {
	switch value := raw.(type) {
	case float64:
		d := int(value)
		if d >= 1 && d <= 4 {
			return d
		}
	case int:
		if value >= 1 && value <= 4 {
			return value
		}
	case string:
		d, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && d >= 1 && d <= 4 {
			return d
		}
	}
	return 2
}

func anyToStringSlice(raw any) []string {
	if raw == nil {
		return []string{}
	}
	switch value := raw.(type) {
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return value
	case string:
		return []string{value}
	default:
		return []string{}
	}
}

func anyToStringMap(raw any) map[string]string {
	out := map[string]string{}
	if raw == nil {
		return out
	}
	mapAny, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for key, value := range mapAny {
		switch v := value.(type) {
		case string:
			out[key] = v
		case float64:
			out[key] = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			if v {
				out[key] = "true"
			} else {
				out[key] = "false"
			}
		}
	}
	return out
}

// ParseQuestion uses an LLM call to structure the extracted question text.
// On error or invalid response, returns a minimal structure with the original question.
func ParseQuestion(ctx context.Context, client *llm.Client, extractedText string) (*ParsedQuestion, error) {
	messages := []llm.ChatMessage{
		{Role: "system", Content: parsingPrompt},
		{Role: "user", Content: extractedText},
	}

	comp, err := client.Complete(ctx, messages)
	if err != nil {
		log.Printf("[parser] LLM call failed: %v — using raw text fallback", err)
		return &ParsedQuestion{
			Subject:    "",
			Chapter:    "",
			Topics:     []string{},
			Difficulty: 2,
			Question:   extractedText,
			Variables:  map[string]string{},
		}, nil
	}

	raw := strings.TrimSpace(comp.Content)
	// Strip code fences if present
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 3 {
			raw = strings.Join(lines[1:len(lines)-1], "\n")
			raw = strings.TrimSpace(raw)
		}
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		log.Printf("[parser] JSON parse failed: %v — using raw text fallback", err)
		return &ParsedQuestion{
			Subject:    "",
			Chapter:    "",
			Topics:     []string{},
			Difficulty: 2,
			Question:   extractedText,
			Variables:  map[string]string{},
		}, nil
	}

	chapterRaw := ""
	if value, ok := payload["chapter"]; ok {
		if s, ok2 := value.(string); ok2 {
			chapterRaw = s
		}
	}
	if chapterRaw == "" {
		if value, ok := payload["main_topic"]; ok {
			if s, ok2 := value.(string); ok2 {
				chapterRaw = s
			}
		}
	}

	topicsRaw := anyToStringSlice(payload["topics"])
	if len(topicsRaw) == 0 {
		topicsRaw = anyToStringSlice(payload["subtopics"])
	}

	question := extractedText
	if value, ok := payload["question"]; ok {
		if s, ok2 := value.(string); ok2 && strings.TrimSpace(s) != "" {
			question = s
		}
	}

	subject := ""
	if value, ok := payload["subject"]; ok {
		if s, ok2 := value.(string); ok2 && strings.TrimSpace(s) != "" {
			subject = s
		}
	}

	canonTopics := canonicalTopics(topicsRaw)
	chapter := inferChapter(chapterRaw, topicsRaw)
	subject = inferSubject(subject, chapter)

	parsed := ParsedQuestion{
		Subject:    subject,
		Chapter:    chapter,
		Topics:     canonTopics,
		Difficulty: parseDifficulty(payload["difficulty"]),
		Question:   question,
		Variables:  anyToStringMap(payload["variables"]),
	}

	// Ensure question is not empty
	if parsed.Question == "" {
		parsed.Question = extractedText
	}
	if parsed.Variables == nil {
		parsed.Variables = map[string]string{}
	}
	if parsed.Topics == nil {
		parsed.Topics = []string{}
	}

	log.Printf("[parser] subject=%s chapter=%q topics=%v difficulty=%d variables=%d tokens=%d",
		parsed.Subject, parsed.Chapter, parsed.Topics, parsed.Difficulty, len(parsed.Variables), comp.Usage.TotalTokens)

	return &parsed, nil
}

// StructuredQuestionContext returns a formatted string for downstream agents
// (hint, solver, verifier) that includes the parsed structure.
func StructuredQuestionContext(pq *ParsedQuestion) string {
	var sb strings.Builder
	topicsStr := strings.Join(pq.Topics, ", ")
	if topicsStr == "" {
		topicsStr = pq.Chapter
	} else if pq.Chapter != "" {
		topicsStr = pq.Chapter + ": " + topicsStr
	}
	subject := pq.Subject
	if subject == "" {
		subject = "General"
	}
	sb.WriteString(fmt.Sprintf("QUESTION (%s - Topics: %s, Difficulty: %d/4):\n\n",
		subject, topicsStr, pq.Difficulty))
	sb.WriteString(pq.Question)
	if len(pq.Variables) > 0 {
		sb.WriteString("\n\nGIVEN DATA:\n")
		for k, v := range pq.Variables {
			sb.WriteString(fmt.Sprintf("- %s = %s\n", k, v))
		}
	}
	return sb.String()
}
