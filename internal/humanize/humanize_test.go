package humanize

import "testing"

func TestExplainKnownPatterns(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		title string
	}{
		{
			name:  "yandex blocked client id",
			raw:   "error listing: [410 - GoneError] Gone",
			title: "Яндекс отключил этот способ входа",
		},
		{
			name:  "refresh token",
			raw:   "unable to get RefreshToken",
			title: "Нужно подключить облако заново",
		},
		{
			name:  "account without mail",
			raw:   "[403 - DiskUnsupportedUserAccountTypeError] User account type is not supported.",
			title: "Этот аккаунт Яндекса не подойдёт",
		},
		{
			name:  "unauthorized",
			raw:   "HTTP 401 Unauthorized",
			title: "Нужно снова войти в облако",
		},
		{
			name:  "rate limit",
			raw:   "429 Too Many Requests",
			title: "Облако ограничило скорость",
		},
		{
			name:  "disk full",
			raw:   "write /downloads/a.bin: no space left on device",
			title: "На сервере закончилось место",
		},
		{
			name:  "long name",
			raw:   "open /downloads/очень-длинное-имя: file name too long",
			title: "Слишком длинное имя файла",
		},
		{
			name:  "quota",
			raw:   "403 quota exceeded: место на Диске закончилось",
			title: "В облаке нет свободного места",
		},
		{
			name:  "forbidden",
			raw:   "403 Forbidden",
			title: "Нет доступа к папке",
		},
		{
			name:  "not found",
			raw:   "directory not found",
			title: "Папка не найдена",
		},
		{
			name:  "network",
			raw:   "net/http: timeout awaiting response headers",
			title: "Нет связи с облаком",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Explain(tc.raw)
			if got.Title != tc.title {
				t.Fatalf("title = %q, want %q (advice %q)", got.Title, tc.title, got.Advice)
			}
			if got.Advice == "" {
				t.Fatalf("advice is empty for %q", tc.raw)
			}
		})
	}
}

func TestExplainUnknownAndEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", "some unexpected rclone failure"} {
		got := Explain(raw)
		if got.Title != "" || got.Advice != "" {
			t.Fatalf("%q: expected empty explanation, got %+v", raw, got)
		}
	}
}

func TestExplainDigitsInPathNotMistakenFor410(t *testing.T) {
	got := Explain("open /downloads/410/file.bin: no space left on device")
	if got.Title != "На сервере закончилось место" {
		t.Fatalf("title = %q", got.Title)
	}
}
