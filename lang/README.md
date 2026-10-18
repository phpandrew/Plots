# Translations

Each file here translates Plots into one language. The English text is the key, the translation is the value:

```json
"Back up now": "Sauvegarder maintenant",
```

Plots follows the Windows display language, or the **Language** setting under **General**. Anything missing from a file falls back to English.

## Fixing a translation

1. Edit the value in your language's file. Leave the English key as it is.
2. Keep placeholders exactly as they are and in the same order: `%s`, `%d`, `%.1f`.
3. `&&` shows as a single `&`. Keep the same number of them as the English.
4. Run `go test ./...`. It checks that every file covers every string and keeps its placeholders.
5. Open a pull request.

## Adding a language

1. Run `PLOTS_I18N_DUMP=lang/xx.json go test -run TestTranslations .` to get a file with every English string and empty values.
2. Fill in the values.
3. Add the language to `allLanguages` in `i18n.go` and its Windows language ID to `LanguageForLangID`.

The terms each language uses for a "plot" are listed below so translations stay consistent.

| Language | "plot" | "live folder" | "tab" | "Peek" |
|---|---|---|---|---|
| Français | parcelle | dossier dynamique | onglet | Aperçu |
| Español | parcela | carpeta dinámica | pestaña | Vistazo |
| Português (Brasil) | lote | pasta dinâmica | guia | Espiar |
| Deutsch | Bereich | Live-Ordner | Registerkarte | Spähen |
| 中文（简体） | 分区 | 实时文件夹 | 标签页 | 速览 |
| 日本語 | 区画 | ライブ フォルダー | タブ | ピーク |
| Русский | участок | живая папка | вкладка | Быстрый просмотр |
