package processor

import "fmt"

// unlikelyCommentTemplates maps UI locale codes to format strings for the
// automatic comment added when the ultrasonic validation filter tags a bat
// detection as unlikely. The two format verbs are CV value (%.3f) and
// threshold (%.2f).
//
//nolint:misspell // Foreign language translations; false positives from English spell checker.
var unlikelyCommentTemplates = map[string]string{
	"da": "Ultralydsvalidering: lav tidsmæssig variation i ultralydsfrekvensområdet (CV: %.3f, tærskel: %.2f). Ingen signifikant flagermusekkolokalisering detekteret i lyden.",
	"de": "Ultraschallvalidierung: geringe zeitliche Variabilität im Ultraschallfrequenzbereich (CV: %.3f, Schwellenwert: %.2f). Keine signifikante Fledermaus-Echoortung im Audio erkannt.",
	"en": "Ultrasonic validation: low temporal variability in ultrasonic frequency range (CV: %.3f, threshold: %.2f). No significant bat echolocation activity detected in the audio.",
	"es": "Validación ultrasónica: baja variabilidad temporal en el rango de frecuencia ultrasónica (CV: %.3f, umbral: %.2f). No se detectó actividad significativa de ecolocalización de murciélagos en el audio.",
	"fi": "Ultraäänivalidointi: matala ajallinen vaihtelu ultraäänitaajuusalueella (CV: %.3f, kynnys: %.2f). Merkittävää lepakon kaikuluotaustoimintaa ei havaittu äänessä.",
	"fr": "Validation ultrasonique : faible variabilité temporelle dans la plage de fréquences ultrasoniques (CV : %.3f, seuil : %.2f). Aucune activité significative d'écholocalisation de chauve-souris détectée dans l'audio.",
	"hu": "Ultrahangos validáció: alacsony időbeli variabilitás az ultrahang-frekvenciatartományban (CV: %.3f, küszöb: %.2f). Nem észlelhető jelentős denevér-echolokációs tevékenység a hangfelvételben.",
	"it": "Validazione ultrasonica: bassa variabilità temporale nella gamma di frequenze ultrasoniche (CV: %.3f, soglia: %.2f). Nessuna attività significativa di ecolocalizzazione di pipistrelli rilevata nell'audio.",
	"lv": "Ultraskaņas validācija: zema laiciskā mainība ultraskaņas frekvenču diapazonā (CV: %.3f, slieksnis: %.2f). Audio nav konstatēta nozīmīga sikspārņu eholokācijas aktivitāte.",
	"nl": "Ultrasone validatie: lage temporele variabiliteit in het ultrasone frequentiebereik (CV: %.3f, drempel: %.2f). Geen significante echolocatie-activiteit van vleermuizen gedetecteerd in de audio.",
	"pl": "Walidacja ultradźwiękowa: niska zmienność czasowa w zakresie częstotliwości ultradźwiękowych (CV: %.3f, próg: %.2f). Nie wykryto znaczącej aktywności echolokacyjnej nietoperzy w nagraniu.",
	"pt": "Validação ultrassônica: baixa variabilidade temporal na faixa de frequência ultrassônica (CV: %.3f, limiar: %.2f). Nenhuma atividade significativa de ecolocalização de morcegos detectada no áudio.",
	"sk": "Ultrazvuková validácia: nízka časová variabilita v ultrazvukovom frekvenčnom rozsahu (CV: %.3f, prah: %.2f). V zvukovom zázname nebola zistená žiadna významná echolokačná aktivita netopierov.",
	"sv": "Ultraljudsvalidering: låg tidsmässig variabilitet i ultraljudsfrekvensområdet (CV: %.3f, tröskel: %.2f). Ingen signifikant ekolokaliseringsaktivitet från fladdermöss detekterad i ljudet.",
}

// defaultUnlikelyCommentLocale is the fallback locale when the configured
// dashboard locale has no translation.
const defaultUnlikelyCommentLocale = "en"

// formatUnlikelyComment returns a localized comment explaining why a detection
// was tagged as unlikely by the ultrasonic validation filter.
func formatUnlikelyComment(locale string, cv, threshold float64) string {
	tmpl, ok := unlikelyCommentTemplates[locale]
	if !ok {
		tmpl = unlikelyCommentTemplates[defaultUnlikelyCommentLocale]
	}
	return fmt.Sprintf(tmpl, cv, threshold)
}
