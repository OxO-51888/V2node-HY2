package officialhy2

import "testing"

func TestMasqSiteByPort(t *testing.T) {
	tests := map[int]string{
		51801: "gm",
		51802: "nnm",
		51803: "ovo",
		51804: "yiyuan",
		51805: "clash",
		51806: "pianyi",
		443:   "",
	}

	for port, want := range tests {
		if got := masqSiteByPort(port); got != want {
			t.Fatalf("masqSiteByPort(%d) = %q, want %q", port, got, want)
		}
	}
}

func TestMasqSiteByName(t *testing.T) {
	tests := map[string]string{
		"gm.example.com":      "gm",
		"nnm.example.com":     "nnm",
		"ovo.example.com":     "ovo",
		"yiyuan.example.com":  "yiyuan",
		"clash.example.com":   "clash",
		"pianyi.example.com":  "pianyi",
		"51801.example.com":   "gm",
		"51806.example.com":   "pianyi",
		"unknown.example.com": "",
		"clash.example.com.":  "clash",
		"  ovo.example.com  ": "ovo",
	}

	for name, want := range tests {
		if got := masqSiteByName(name); got != want {
			t.Fatalf("masqSiteByName(%q) = %q, want %q", name, got, want)
		}
	}
}
