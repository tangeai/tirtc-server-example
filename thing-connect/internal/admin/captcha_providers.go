package admin

import "strings"

type captchaProviderSpec struct {
	Code                      string
	Label                     string
	RequiredSecrets           []string
	RequiresMiniProgramSecret bool
}

var captchaProviderCatalog = []captchaProviderSpec{
	{Code: "yidun", Label: "网易易盾", RequiredSecrets: []string{"secret_id", "secret_key"}},
	{Code: "geetest", Label: "极验", RequiredSecrets: []string{"secret_key"}, RequiresMiniProgramSecret: true},
	{Code: "aliyun", Label: "阿里云", RequiredSecrets: []string{"secret_id", "secret_key"}},
	{Code: "tencent", Label: "腾讯云", RequiredSecrets: []string{"secret_id", "secret_key", "app_secret_key"}, RequiresMiniProgramSecret: true},
}

func captchaProviderSpecFor(code string) (captchaProviderSpec, bool) {
	for _, provider := range captchaProviderCatalog {
		if provider.Code == code {
			return provider, true
		}
	}
	return captchaProviderSpec{}, false
}

func captchaProviderOptions() []ConfigFieldOption {
	options := make([]ConfigFieldOption, 0, len(captchaProviderCatalog))
	for _, provider := range captchaProviderCatalog {
		options = append(options, ConfigFieldOption{Label: provider.Label, Value: provider.Code})
	}
	return options
}

func captchaProviderLabels() string {
	labels := make([]string, 0, len(captchaProviderCatalog))
	for _, provider := range captchaProviderCatalog {
		labels = append(labels, provider.Label)
	}
	return strings.Join(labels, "、")
}
