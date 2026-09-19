package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"
)

type IntelligentTestEvaluation struct {
	Status string
	Score  *float64
	Image  string
	Detail map[string]any
}

type IntelligentTestEvaluator interface {
	Evaluate(output string, cfg IntelligentTestConfig) IntelligentTestEvaluation
}

func DefaultIntelligentTestEvaluators() map[string]IntelligentTestEvaluator {
	return map[string]IntelligentTestEvaluator{
		"exact_answer":  exactAnswerEvaluator{},
		"svg_structure": svgStructureEvaluator{},
	}
}

type exactAnswerEvaluator struct{}

var intelligentAnswerLine = regexp.MustCompile(`(?im)^\s*ANSWER\s*[:：]\s*([^\r\n]+)\s*$`)

func (exactAnswerEvaluator) Evaluate(output string, cfg IntelligentTestConfig) IntelligentTestEvaluation {
	detail := newIntelligentAssessment("exact_answer")
	actual := strings.TrimSpace(output)
	if matches := intelligentAnswerLine.FindStringSubmatch(output); len(matches) == 2 {
		actual = strings.TrimSpace(matches[1])
	}
	expected := strings.TrimSpace(cfg.ExpectedAnswer)
	detail["expected_answer"] = expected
	detail["actual_answer"] = actual
	if normalizeIntelligentAnswer(actual) == normalizeIntelligentAnswer(expected) {
		score := 100.0
		detail["answer_verdict"] = "correct"
		detail["format_verdict"] = "compliant"
		return IntelligentTestEvaluation{Status: IntelligentTestStatusSuccess, Score: &score, Detail: detail}
	}
	score := 0.0
	detail["answer_verdict"] = "incorrect"
	detail["format_verdict"] = "non_compliant"
	return IntelligentTestEvaluation{Status: IntelligentTestStatusSuspectedDegradation, Score: &score, Detail: detail}
}

func normalizeIntelligentAnswer(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".")
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "颗")
	return strings.TrimSpace(value)
}

type svgStructureEvaluator struct{}

func (svgStructureEvaluator) Evaluate(output string, _ IntelligentTestConfig) IntelligentTestEvaluation {
	detail := newIntelligentAssessment("svg_structure")
	svg, err := SanitizeIntelligentTestSVG(output)
	if err != nil {
		detail["answer_verdict"] = "not_evaluated"
		detail["format_verdict"] = "non_compliant"
		detail["format_reason"] = err.Error()
		return IntelligentTestEvaluation{Status: IntelligentTestStatusSuspectedDegradation, Detail: detail}
	}
	detail["answer_verdict"] = "not_evaluated"
	detail["format_verdict"] = "compliant"
	detail["image_state"] = "ready"
	detail["limitation"] = "SVG was structurally sanitized; semantic drawing quality requires manual review."
	return IntelligentTestEvaluation{Status: IntelligentTestStatusCompleted, Image: svg, Detail: detail}
}

func newIntelligentAssessment(evaluator string) map[string]any {
	return map[string]any{
		"evaluator":        evaluator,
		"execution_status": "completed",
		"answer_verdict":   "not_evaluated",
		"format_verdict":   "not_evaluated",
	}
}

var intelligentSVGElements = map[string]bool{
	"svg": true, "g": true, "path": true, "rect": true, "circle": true, "ellipse": true,
	"line": true, "polyline": true, "polygon": true, "title": true, "desc": true,
}

var intelligentSVGAttributes = map[string]bool{
	"xmlns": true, "viewBox": true, "width": true, "height": true, "x": true, "y": true,
	"x1": true, "x2": true, "y1": true, "y2": true, "cx": true, "cy": true, "r": true,
	"rx": true, "ry": true, "d": true, "points": true, "fill": true, "stroke": true,
	"stroke-width": true, "stroke-linecap": true, "stroke-linejoin": true, "opacity": true,
	"transform": true, "aria-label": true, "role": true,
}

func SanitizeIntelligentTestSVG(output string) (string, error) {
	start := strings.Index(output, "<svg")
	end := strings.LastIndex(output, "</svg>")
	if start < 0 || end < start {
		return "", errors.New("missing complete svg")
	}
	raw := output[start : end+len("</svg>")]
	if len(raw) > 128<<10 {
		return "", errors.New("svg exceeds 128 KiB")
	}
	decoder := xml.NewDecoder(strings.NewReader(raw))
	var out bytes.Buffer
	encoder := xml.NewEncoder(&out)
	depth, nodes, shapes, roots := 0, 0, 0, 0
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", errors.New("invalid svg xml")
		}
		switch token := tok.(type) {
		case xml.StartElement:
			depth++
			nodes++
			if depth == 1 {
				roots++
				if roots > 1 || token.Name.Local != "svg" {
					return "", errors.New("invalid svg root")
				}
			}
			if depth > 40 || nodes > 6000 {
				return "", errors.New("svg is too complex")
			}
			if !intelligentSVGElements[token.Name.Local] || (token.Name.Space != "" && token.Name.Space != "http://www.w3.org/2000/svg") {
				return "", errors.New("svg contains disallowed elements")
			}
			safeAttrs := make([]xml.Attr, 0, len(token.Attr)+1)
			if depth == 1 {
				safeAttrs = append(safeAttrs, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
			}
			for _, attr := range token.Attr {
				if attr.Name.Local == "xmlns" && attr.Value == "http://www.w3.org/2000/svg" {
					continue
				}
				if attr.Name.Space != "" || !intelligentSVGAttributes[attr.Name.Local] {
					return "", errors.New("svg contains disallowed attributes")
				}
				if !intelligentSafeSVGValue(attr.Value) {
					return "", errors.New("svg contains active or external content")
				}
				safeAttrs = append(safeAttrs, xml.Attr{Name: xml.Name{Local: attr.Name.Local}, Value: attr.Value})
			}
			if strings.Contains(" path rect circle ellipse line polyline polygon ", " "+token.Name.Local+" ") {
				shapes++
			}
			token.Name.Space = ""
			token.Attr = safeAttrs
			if err := encoder.EncodeToken(token); err != nil {
				return "", err
			}
		case xml.EndElement:
			depth--
			token.Name.Space = ""
			if err := encoder.EncodeToken(token); err != nil {
				return "", err
			}
		case xml.CharData:
			if len(bytes.TrimSpace(token)) > 0 && depth > 1 {
				if err := encoder.EncodeToken(token); err != nil {
					return "", err
				}
			}
		case xml.Comment:
		default:
			return "", errors.New("svg contains unsupported xml content")
		}
	}
	if depth != 0 || shapes == 0 {
		return "", errors.New("svg has no drawable shapes")
	}
	if err := encoder.Flush(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func intelligentSafeSVGValue(value string) bool {
	lower := strings.ToLower(value)
	if strings.ContainsAny(value, "<>\x00") {
		return false
	}
	blocked := []string{"javascript:", "data:", "http:", "https:", "//", "<script", "onload", "onclick"}
	for _, token := range blocked {
		if strings.Contains(lower, token) {
			return false
		}
	}
	return true
}
