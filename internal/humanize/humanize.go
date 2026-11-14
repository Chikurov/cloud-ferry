// Package humanize turns rclone and API errors into short, actionable Russian
// explanations. The raw error always stays available; the hint only helps.
package humanize

import "strings"

// Explanation is a short title plus a concrete next step.
type Explanation struct {
	Title  string
	Advice string
}

// Explain matches known failure patterns. Unknown errors return an empty
// Explanation so the caller can show the raw text unchanged.
func Explain(raw string) Explanation {
	text := strings.TrimSpace(raw)
	if text == "" {
		return Explanation{}
	}
	lower := strings.ToLower(text)

	switch {
	case strings.Contains(lower, "410 -") || strings.Contains(lower, "410 gone") || strings.Contains(lower, "goneerror"):
		return Explanation{
			Title:  "Яндекс отключил этот способ входа",
			Advice: "Отключите облако и подключите его заново — по паролю приложения или через своё приложение в Яндекс ID.",
		}
	case strings.Contains(lower, "unable to get refreshtoken") || strings.Contains(lower, "refreshtoken"):
		return Explanation{
			Title:  "Нужно подключить облако заново",
			Advice: "Срок действия доступа истёк. Отключите облако и подключите его снова.",
		}
	case strings.Contains(lower, "diskunsupporteduseraccounttype"):
		return Explanation{
			Title:  "Этот аккаунт Яндекса не подойдёт",
			Advice: "Для Диска нужен аккаунт с Яндекс Почтой.",
		}
	case strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized"):
		return Explanation{
			Title:  "Нужно снова войти в облако",
			Advice: "Пароль приложения или доступ больше не действуют. Отключите облако и подключите его заново.",
		}
	case strings.Contains(lower, "429") || strings.Contains(lower, "too many requests"):
		return Explanation{
			Title:  "Облако ограничило скорость",
			Advice: "Подождите несколько минут и нажмите «Продолжить». Если повторится — уменьшите число одновременных файлов в настройках.",
		}
	case strings.Contains(lower, "enospc") || strings.Contains(lower, "no space left") || strings.Contains(lower, "insufficient storage"):
		return Explanation{
			Title:  "На сервере закончилось место",
			Advice: "Освободите место или выберите папку на внешнем диске, затем нажмите «Продолжить».",
		}
	case strings.Contains(lower, "enametoolong") || strings.Contains(lower, "file name too long"):
		return Explanation{
			Title:  "Слишком длинное имя файла",
			Advice: "Файл с таким именем не помещается на сервер. Переименуйте его в облаке и нажмите «Продолжить».",
		}
	case strings.Contains(lower, "403") && (strings.Contains(lower, "quota") || strings.Contains(lower, "место") || strings.Contains(lower, "space")):
		return Explanation{
			Title:  "В облаке нет свободного места",
			Advice: "Освободите место в облаке и нажмите «Продолжить».",
		}
	case strings.Contains(lower, "403"):
		return Explanation{
			Title:  "Нет доступа к папке",
			Advice: "Проверьте, что у подключения есть доступ к этой папке, или выберите другую.",
		}
	case strings.Contains(lower, "404") || strings.Contains(lower, "not found"):
		return Explanation{
			Title:  "Папка не найдена",
			Advice: "Проверьте папку в облаке: её могли переименовать или удалить.",
		}
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host"):
		return Explanation{
			Title:  "Нет связи с облаком",
			Advice: "Проверьте интернет на сервере и нажмите «Продолжить». Скачанные файлы не будут скачаны снова.",
		}
	}
	return Explanation{}
}
