// Pairline - Open Source Video Chat and Matchmaking
// Copyright (C) 2026 Albert Blasczykowski
// Aless Microsystems
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package taxonomy

import "strings"

type Category struct {
	Code      string
	Label     string
	Canonical string
	Aliases   []string
}

var categories = []Category{
	{Code: "S1", Label: "Violence", Canonical: "violence"},
	{Code: "S2", Label: "Sexual", Canonical: "sexual"},
	{Code: "S3", Label: "Criminal Planning/Confessions", Canonical: "criminal planning/confessions"},
	{Code: "S4", Label: "Guns and Illegal Weapons", Canonical: "guns and illegal weapons"},
	{Code: "S5", Label: "Controlled/Regulated Substances", Canonical: "controlled/regulated substances"},
	{Code: "S6", Label: "Suicide and Self Harm", Canonical: "suicide and self harm", Aliases: []string{"suicide & self-harm", "suicide/self-harm"}},
	{Code: "S7", Label: "Sexual (minor)", Canonical: "sexual (minor)", Aliases: []string{"child sexual exploitation"}},
	{Code: "S8", Label: "Hate/Identity Hate", Canonical: "hate/identity hate", Aliases: []string{"hate"}},
	{Code: "S9", Label: "PII/Privacy", Canonical: "pii/privacy", Aliases: []string{"privacy", "pii"}},
	{Code: "S10", Label: "Harassment", Canonical: "harassment", Aliases: []string{"sexual harassment"}},
	{Code: "S11", Label: "Threat", Canonical: "threat", Aliases: []string{"threats", "intimidation"}},
	{Code: "S12", Label: "Profanity", Canonical: "profanity"},
	{Code: "S13", Label: "Needs Caution", Canonical: "needs caution"},
	{Code: "S14", Label: "Other", Canonical: "other", Aliases: []string{"defamation"}},
	{Code: "S15", Label: "Manipulation", Canonical: "manipulation"},
	{Code: "S16", Label: "Fraud/Deception", Canonical: "fraud/deception", Aliases: []string{"fraud", "scams", "deception"}},
	{Code: "S17", Label: "Malware", Canonical: "malware", Aliases: []string{"code interpreter abuse", "cyber crimes", "hacking"}},
	{Code: "S18", Label: "High Risk Gov Decision Making", Canonical: "high risk gov decision making"},
	{Code: "S19", Label: "Political/Misinformation/Conspiracy", Canonical: "political/misinformation/conspiracy", Aliases: []string{"elections"}},
	{Code: "S20", Label: "Copyright/Trademark/Plagiarism", Canonical: "copyright/trademark/plagiarism", Aliases: []string{"intellectual property"}},
	{Code: "S21", Label: "Unauthorized Advice", Canonical: "unauthorized advice", Aliases: []string{"specialized advice"}},
	{Code: "S22", Label: "Illegal Activity", Canonical: "illegal activity", Aliases: []string{"violent crimes", "non-violent crimes", "sex-related crimes"}},
	{Code: "S23", Label: "Immoral/Unethical", Canonical: "immoral/unethical"},
}

var lookup = buildLookup()

func Render() string {
	var b strings.Builder
	for i, category := range categories {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(category.Code)
		b.WriteString(":")
		b.WriteString(category.Label)
	}
	b.WriteString("\n")
	return b.String()
}

func Canonicalize(raw string) string {
	key := normalizeKey(raw)
	if key == "" {
		return ""
	}
	if canonical, ok := lookup[key]; ok {
		return canonical
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func CanonicalizeMany(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		canonical := Canonicalize(value)
		if canonical == "" {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func buildLookup() map[string]string {
	out := make(map[string]string, len(categories)*4)
	for _, category := range categories {
		out[normalizeKey(category.Code)] = category.Canonical
		out[normalizeKey(category.Label)] = category.Canonical
		out[normalizeKey(category.Canonical)] = category.Canonical
		for _, alias := range category.Aliases {
			out[normalizeKey(alias)] = category.Canonical
		}
	}
	return out
}

func normalizeKey(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	trimmed = strings.NewReplacer("&", "and", "_", " ", "-", " ").Replace(trimmed)
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	return trimmed
}
