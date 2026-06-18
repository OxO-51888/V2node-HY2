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
		"gm.5188777.xyz":                            "gm",
		"nnm.5188777.xyz":                           "nnm",
		"ovo.5188777.xyz":                           "ovo",
		"yiyuan.5188777.xyz":                        "yiyuan",
		"clash.5188777.xyz":                         "clash",
		"pianyi.5188777.xyz":                        "pianyi",
		"xn--54qr1i.xn--oor32f63hs9js55d.com":       "gm",
		"xn--i2r10aa.xn--oor32f63hs9js55d.com":      "nnm",
		"xn--4gq62f52gdss.xn--oor32f63hs9js55d.com": "yiyuan",
		"xn--wtq35pfyd55o.xn--oor32f63hs9js55d.com": "pianyi",
		"51801.5188777.xyz":                         "gm",
		"51806.5188777.xyz":                         "pianyi",
		"unknown.5188777.xyz":                       "",
		"clash.5188777.xyz.":                        "clash",
		"  ovo.5188777.xyz  ":                       "ovo",
	}

	for name, want := range tests {
		if got := masqSiteByName(name); got != want {
			t.Fatalf("masqSiteByName(%q) = %q, want %q", name, got, want)
		}
	}
}
