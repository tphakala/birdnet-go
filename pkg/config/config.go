// config/config.go
package config

type Settings struct {
	InputAudioFile string
	RealtimeMode   bool
	ModelPath      string
	Sensitivity    float64
	Overlap        float64
	Debug          bool
	CapturePath    string
	LogPath        string
	Threshold      float64
	Locale         string
	ProcessingTime bool
}

var Locales = map[string]string{
	"Afrikaans": "labels_af.txt",
	"Catalan":   "labels_ca.txt",
	"Czech":     "labels_cs.txt",
	"Chinese":   "labels_zh.txt",
	"Croatian":  "labels_hr.txt",
	"Danish":    "labels_da.txt",
	"Dutch":     "labels_nl.txt",
	"English":   "labels_en.txt",
	"Estonian":  "labels_et.txt",
	"Finnish":   "labels_fi.txt",
	"French":    "labels_fr.txt",
	"German":    "labels_de.txt",
	"Hungarian": "labels_hu.txt",
	"Icelandic": "labels_is.txt",
	"Indonesia": "labels_id.txt",
	"Italian":   "labels_it.txt",
	"Japanese":  "labels_ja.txt",
	"Latvian":   "labels_lv.txt",
	"Lithuania": "labels_lt.txt",
	"Norwegian": "labels_no.txt",
	"Polish":    "labels_pl.txt",
	"Portugues": "labels_pt.txt",
	"Russian":   "labels_ru.txt",
	"Slovak":    "labels_sk.txt",
	"Slovenian": "labels_sl.txt",
	"Spanish":   "labels_es.txt",
	"Swedish":   "labels_sv.txt",
	"Thai":      "labels_th.txt",
	"Ukrainian": "labels_uk.txt",
}

var LocaleCodes = map[string]string{
	"af": "Afrikaans",
	"ca": "Catalan",
	"cs": "Czech",
	"zh": "Chinese",
	"hr": "Croatian",
	"da": "Danish",
	"nl": "Dutch",
	"en": "English",
	"et": "Estonian",
	"fi": "Finnish",
	"fr": "French",
	"de": "German",
	"hu": "Hungarian",
	"is": "Icelandic",
	"id": "Indonesia",
	"it": "Italian",
	"ja": "Japanese",
	"lv": "Latvian",
	"lt": "Lithuania",
	"no": "Norwegian",
	"pl": "Polish",
	"pt": "Portugues",
	"ru": "Russian",
	"sk": "Slovak",
	"sl": "Slovenian",
	"es": "Spanish",
	"sv": "Swedish",
	"th": "Thai",
	"uk": "Ukrainian",
}
