package processor

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/detection"
)

// unlikelyCommentTemplates maps UI locale codes to format strings for the
// automatic comment added when the ultrasonic validation filter tags a bat
// detection as unlikely (every hit window judged flat). The two format verbs are CV value (%.3f) and
// threshold (%.2f).
//
//nolint:misspell // Foreign language translations; false positives from English spell checker.
var unlikelyCommentTemplates = map[string]string{
	"cs": "Ultrazvukové ověření: zvuková okna, ze kterých tato detekce vznikla, nevykazují echolokační aktivitu netopýrů (nejvyšší ultrazvukové CV: %.3f, práh: %.2f). Hlasy netopýrů jinde v nahrávce nebyly určeny jako tento druh.",
	"da": "Ultralydsvalidering: lydvinduerne, der gav denne detektion, viser ingen flagermusekkolokalisering (højeste ultralyds-CV: %.3f, tærskel: %.2f). Flagermuskald andre steder i optagelsen blev ikke identificeret som denne art.",
	"de": "Ultraschallvalidierung: Die Audiofenster, die diese Erkennung ausgelöst haben, zeigen keine Fledermaus-Echoortung (höchster Ultraschall-CV: %.3f, Schwellenwert: %.2f). Fledermausrufe an anderer Stelle der Aufnahme wurden nicht als diese Art identifiziert.",
	"en": "Ultrasonic validation: the audio windows that produced this detection show no bat echolocation activity (highest ultrasonic CV: %.3f, threshold: %.2f). Any bat calls elsewhere in the recording were not identified as this species.",
	"es": "Validación ultrasónica: las ventanas de audio que produjeron esta detección no muestran actividad de ecolocalización de murciélagos (CV ultrasónico más alto: %.3f, umbral: %.2f). Las llamadas de murciélagos en otras partes de la grabación no se identificaron como esta especie.",
	"fi": "Ultraäänivalidointi: tämän havainnon tuottaneissa ääni-ikkunoissa ei ole lepakoiden kaikuluotausta (korkein ultraääni-CV: %.3f, kynnys: %.2f). Muualla tallenteessa olevia lepakon ääniä ei tunnistettu tähän lajiin.",
	"fr": "Validation ultrasonique : les fenêtres audio à l'origine de cette détection ne montrent aucune activité d'écholocalisation de chauve-souris (CV ultrasonique le plus élevé : %.3f, seuil : %.2f). Les cris de chauve-souris ailleurs dans l'enregistrement n'ont pas été identifiés comme cette espèce.",
	"hu": "Ultrahangos validáció: az észlelést eredményező hangablakokban nincs denevér-echolokációs tevékenység (legmagasabb ultrahang-CV: %.3f, küszöb: %.2f). A felvétel más részein hallható denevérhangokat nem azonosították erre a fajra.",
	"it": "Validazione ultrasonica: le finestre audio che hanno prodotto questo rilevamento non mostrano attività di ecolocalizzazione di pipistrelli (CV ultrasonico più alto: %.3f, soglia: %.2f). I richiami di pipistrelli in altri punti della registrazione non sono stati identificati come questa specie.",
	"lv": "Ultraskaņas validācija: audio logos, kas radīja šo konstatējumu, nav sikspārņu eholokācijas aktivitātes (augstākais ultraskaņas CV: %.3f, slieksnis: %.2f). Sikspārņu saucieni citur ierakstā netika identificēti kā šī suga.",
	"nb": "Ultralydvalidering: lydvinduene som ga denne deteksjonen, viser ingen ekkolokalisering fra flaggermus (høyeste ultralyd-CV: %.3f, terskel: %.2f). Flaggermuslyder andre steder i opptaket ble ikke identifisert som denne arten.",
	"nl": "Ultrasone validatie: de audiovensters die deze detectie opleverden tonen geen echolocatie-activiteit van vleermuizen (hoogste ultrasone CV: %.3f, drempel: %.2f). Vleermuisroepen elders in de opname zijn niet als deze soort geïdentificeerd.",
	"pl": "Walidacja ultradźwiękowa: okna audio, które dały to wykrycie, nie wykazują echolokacji nietoperzy (najwyższe ultradźwiękowe CV: %.3f, próg: %.2f). Odgłosy nietoperzy w innych miejscach nagrania nie zostały zidentyfikowane jako ten gatunek.",
	"pt": "Validação ultrassônica: as janelas de áudio que produziram esta detecção não mostram atividade de ecolocalização de morcegos (CV ultrassônico mais alto: %.3f, limiar: %.2f). Chamados de morcegos em outras partes da gravação não foram identificados como esta espécie.",
	"sk": "Ultrazvuková validácia: zvukové okná, ktoré vytvorili túto detekciu, nevykazujú echolokačnú aktivitu netopierov (najvyššie ultrazvukové CV: %.3f, prah: %.2f). Volania netopierov inde v zázname neboli identifikované ako tento druh.",
	"sv": "Ultraljudsvalidering: ljudfönstren som gav denna detektion visar ingen ekolokaliseringsaktivitet från fladdermöss (högsta ultraljuds-CV: %.3f, tröskel: %.2f). Fladdermusläten på andra ställen i inspelningen identifierades inte som denna art.",
}

// defaultUnlikelyCommentLocale is the fallback locale when the configured
// dashboard locale has no translation.
const defaultUnlikelyCommentLocale = "en"

// formatUnlikelyComment returns a localized comment explaining why a detection
// was tagged as unlikely. The ultrasonic CV filter is the only validator so far, so
// its template serves every result whatever result.UnlikelySources holds; the
// result is passed whole so the text can follow the sources once there are several.
func formatUnlikelyComment(locale string, result *detection.Result) string {
	tmpl, ok := unlikelyCommentTemplates[locale]
	if !ok {
		tmpl = unlikelyCommentTemplates[defaultUnlikelyCommentLocale]
	}
	return fmt.Sprintf(tmpl, result.UltrasonicCV, result.UltrasonicCVThreshold)
}
