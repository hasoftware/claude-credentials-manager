// Package models contains the Codex profiles shared by the gateway and launcher.
package models

import "strings"

type Profile struct {
	ID          string
	Label       string
	Description string
	Upstream    string
}

var Catalog = []Profile{
	{"uthink", "UThink", "UThink Pro 5.5 · Model by HASOFT (GPT-6.2 Sol) · Research, code, review PR", "gpt-6.2-sol"},
	{"udeep", "UDeep · OpenAI", "Debug xuyên dịch vụ, phân tích kiến trúc · GPT-6.2 Astra", "gpt-6.2-astra"},
	{"ulens", "ULens · OpenAI", "Đọc ảnh, kiểm tra UI, thao tác ứng dụng · GPT-6.1 Terra", "gpt-6.1-terra"},
	{"udash", "UDash · OpenAI", "Tóm tắt log, phân loại, trích dữ liệu · GPT-6 Luna", "gpt-6-luna"},
}

func ByAlias(alias string) (Profile, bool) {
	id := strings.TrimSuffix(alias, "-oai")
	for _, profile := range Catalog {
		if id == profile.ID {
			return profile, true
		}
	}
	return Profile{}, false
}

func SupportsControls(upstream string) bool {
	for _, profile := range Catalog {
		if profile.Upstream == upstream {
			return true
		}
	}
	return false
}
