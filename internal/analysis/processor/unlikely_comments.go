package processor

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/analysis/noisebank"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// unlikelyCommentTemplates maps UI locale codes to format strings for the
// automatic comment added when only the ultrasonic validation filter tagged a
// detection as unlikely (every hit window judged flat). The two format verbs are CV
// value (%.3f) and threshold (%.2f).
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

// unlikelyNoiseCommentTemplates is unlikelyCommentTemplates for a detection only
// the noise example gate tagged: every hit window matches a noise example the user
// marked. The two format verbs are the lowest similarity (%.3f) and the match
// threshold (%.2f).
//
//nolint:misspell // Foreign language translations; false positives from English spell checker.
var unlikelyNoiseCommentTemplates = map[string]string{
	"cs": "Ověření podle vzorů šumu: každé zvukové okno, ze kterého tato detekce vznikla, odpovídá vzoru šumu označenému uživatelem (nejnižší podobnost: %.3f, práh: %.2f).",
	"da": "Validering mod støjeksempler: hvert lydvindue, der gav denne detektion, svarer til et støjeksempel markeret af brugeren (laveste lighed: %.3f, tærskel: %.2f).",
	"de": "Validierung anhand von Rauschbeispielen: Jedes Audiofenster, das diese Erkennung ausgelöst hat, stimmt mit einem vom Benutzer markierten Rauschbeispiel überein (geringste Ähnlichkeit: %.3f, Schwellenwert: %.2f).",
	"en": "Noise example validation: every audio window that produced this detection matches a noise example marked by the user (lowest similarity: %.3f, threshold: %.2f).",
	"es": "Validación por ejemplos de ruido: todas las ventanas de audio que produjeron esta detección coinciden con un ejemplo de ruido marcado por el usuario (similitud más baja: %.3f, umbral: %.2f).",
	"fi": "Kohinaesimerkkien validointi: jokainen tämän havainnon tuottanut ääni-ikkuna vastaa käyttäjän merkitsemää kohinaesimerkkiä (alhaisin samankaltaisuus: %.3f, kynnys: %.2f).",
	"fr": "Validation par exemples de bruit : toutes les fenêtres audio à l'origine de cette détection correspondent à un exemple de bruit marqué par l'utilisateur (similarité la plus faible : %.3f, seuil : %.2f).",
	"hu": "Zajminta-validáció: az észlelést eredményező minden hangablak megegyezik egy, a felhasználó által megjelölt zajmintával (legalacsonyabb hasonlóság: %.3f, küszöb: %.2f).",
	"it": "Validazione con esempi di rumore: tutte le finestre audio che hanno prodotto questo rilevamento corrispondono a un esempio di rumore contrassegnato dall'utente (somiglianza più bassa: %.3f, soglia: %.2f).",
	"lv": "Trokšņa piemēru validācija: katrs audio logs, kas radīja šo konstatējumu, atbilst lietotāja atzīmētam trokšņa piemēram (zemākā līdzība: %.3f, slieksnis: %.2f).",
	"nb": "Validering mot støyeksempler: hvert lydvindu som ga denne deteksjonen, samsvarer med et støyeksempel markert av brukeren (laveste likhet: %.3f, terskel: %.2f).",
	"nl": "Validatie met ruisvoorbeelden: elk audiovenster dat deze detectie opleverde, komt overeen met een door de gebruiker gemarkeerd ruisvoorbeeld (laagste gelijkenis: %.3f, drempel: %.2f).",
	"pl": "Walidacja na podstawie przykładów szumu: każde okno audio, które dało to wykrycie, pasuje do przykładu szumu oznaczonego przez użytkownika (najniższe podobieństwo: %.3f, próg: %.2f).",
	"pt": "Validação por exemplos de ruído: todas as janelas de áudio que produziram esta detecção correspondem a um exemplo de ruído marcado pelo usuário (menor similaridade: %.3f, limiar: %.2f).",
	"sk": "Validácia podľa príkladov šumu: každé zvukové okno, ktoré vytvorilo túto detekciu, zodpovedá príkladu šumu označenému používateľom (najnižšia podobnosť: %.3f, prah: %.2f).",
	"sv": "Validering mot brusexempel: varje ljudfönster som gav denna detektion matchar ett brusexempel som användaren har markerat (lägsta likhet: %.3f, tröskel: %.2f).",
}

// unlikelyCombinedCommentTemplates is unlikelyCommentTemplates for a detection both
// validators judged windows of: every hit window was judged to hold no bat call,
// each by one validator or the other. The four format verbs are the highest CV
// (%.3f) and its threshold (%.2f) among the windows the CV filter judged, then the
// lowest similarity (%.3f) and the match threshold (%.2f) among the windows the
// noise examples judged.
//
//nolint:misspell // Foreign language translations; false positives from English spell checker.
var unlikelyCombinedCommentTemplates = map[string]string{
	"cs": "Ultrazvukové ověření a ověření podle vzorů šumu: každé zvukové okno, ze kterého tato detekce vznikla, bylo posouzeno jako okno bez hlasu netopýra, buď proto, že nevykazuje echolokační aktivitu netopýrů (nejvyšší ultrazvukové CV: %.3f, práh: %.2f), nebo proto, že odpovídá vzoru šumu označenému uživatelem (nejnižší podobnost: %.3f, práh: %.2f). Hlasy netopýrů jinde v nahrávce nebyly určeny jako tento druh.",
	"da": "Ultralyds- og støjeksempelvalidering: hvert lydvindue, der gav denne detektion, blev vurderet til ikke at indeholde et flagermuskald, enten fordi det ikke viser flagermusekkolokalisering (højeste ultralyds-CV: %.3f, tærskel: %.2f), eller fordi det svarer til et støjeksempel markeret af brugeren (laveste lighed: %.3f, tærskel: %.2f). Flagermuskald andre steder i optagelsen blev ikke identificeret som denne art.",
	"de": "Ultraschall- und Rauschbeispielvalidierung: Jedes Audiofenster, das diese Erkennung ausgelöst hat, wurde als frei von Fledermausrufen beurteilt, entweder weil es keine Fledermaus-Echoortung zeigt (höchster Ultraschall-CV: %.3f, Schwellenwert: %.2f) oder weil es mit einem vom Benutzer markierten Rauschbeispiel übereinstimmt (geringste Ähnlichkeit: %.3f, Schwellenwert: %.2f). Fledermausrufe an anderer Stelle der Aufnahme wurden nicht als diese Art identifiziert.",
	"en": "Ultrasonic and noise example validation: every audio window that produced this detection was judged to hold no bat call, either because it shows no bat echolocation activity (highest ultrasonic CV: %.3f, threshold: %.2f) or because it matches a noise example marked by the user (lowest similarity: %.3f, threshold: %.2f). Any bat calls elsewhere in the recording were not identified as this species.",
	"es": "Validación ultrasónica y por ejemplos de ruido: todas las ventanas de audio que produjeron esta detección se juzgaron sin llamada de murciélago, bien porque no muestran actividad de ecolocalización de murciélagos (CV ultrasónico más alto: %.3f, umbral: %.2f), bien porque coinciden con un ejemplo de ruido marcado por el usuario (similitud más baja: %.3f, umbral: %.2f). Las llamadas de murciélagos en otras partes de la grabación no se identificaron como esta especie.",
	"fi": "Ultraääni- ja kohinaesimerkkivalidointi: jokaisen tämän havainnon tuottaneen ääni-ikkunan arvioitiin olevan ilman lepakon ääntä joko siksi, että siinä ei ole lepakoiden kaikuluotausta (korkein ultraääni-CV: %.3f, kynnys: %.2f), tai siksi, että se vastaa käyttäjän merkitsemää kohinaesimerkkiä (alhaisin samankaltaisuus: %.3f, kynnys: %.2f). Muualla tallenteessa olevia lepakon ääniä ei tunnistettu tähän lajiin.",
	"fr": "Validation ultrasonique et par exemples de bruit : chaque fenêtre audio à l'origine de cette détection a été jugée sans cri de chauve-souris, soit parce qu'elle ne montre aucune activité d'écholocalisation de chauve-souris (CV ultrasonique le plus élevé : %.3f, seuil : %.2f), soit parce qu'elle correspond à un exemple de bruit marqué par l'utilisateur (similarité la plus faible : %.3f, seuil : %.2f). Les cris de chauve-souris ailleurs dans l'enregistrement n'ont pas été identifiés comme cette espèce.",
	"hu": "Ultrahangos és zajminta-validáció: az észlelést eredményező minden hangablakot denevérhang nélkülinek ítéltek, vagy mert nincs benne denevér-echolokációs tevékenység (legmagasabb ultrahang-CV: %.3f, küszöb: %.2f), vagy mert megegyezik egy, a felhasználó által megjelölt zajmintával (legalacsonyabb hasonlóság: %.3f, küszöb: %.2f). A felvétel más részein hallható denevérhangokat nem azonosították erre a fajra.",
	"it": "Validazione ultrasonica e con esempi di rumore: ogni finestra audio che ha prodotto questo rilevamento è stata giudicata priva di richiami di pipistrello, perché non mostra attività di ecolocalizzazione di pipistrelli (CV ultrasonico più alto: %.3f, soglia: %.2f) oppure perché corrisponde a un esempio di rumore contrassegnato dall'utente (somiglianza più bassa: %.3f, soglia: %.2f). I richiami di pipistrelli in altri punti della registrazione non sono stati identificati come questa specie.",
	"lv": "Ultraskaņas un trokšņa piemēru validācija: katrs audio logs, kas radīja šo konstatējumu, tika atzīts par tādu, kurā nav sikspārņa saucienu, vai nu tāpēc, ka tajā nav sikspārņu eholokācijas aktivitātes (augstākais ultraskaņas CV: %.3f, slieksnis: %.2f), vai tāpēc, ka tas atbilst lietotāja atzīmētam trokšņa piemēram (zemākā līdzība: %.3f, slieksnis: %.2f). Sikspārņu saucieni citur ierakstā netika identificēti kā šī suga.",
	"nb": "Ultralyd- og støyeksempelvalidering: hvert lydvindu som ga denne deteksjonen, ble vurdert til ikke å inneholde flaggermuslyd, enten fordi det ikke viser ekkolokalisering fra flaggermus (høyeste ultralyd-CV: %.3f, terskel: %.2f), eller fordi det samsvarer med et støyeksempel markert av brukeren (laveste likhet: %.3f, terskel: %.2f). Flaggermuslyder andre steder i opptaket ble ikke identifisert som denne arten.",
	"nl": "Ultrasone validatie en validatie met ruisvoorbeelden: elk audiovenster dat deze detectie opleverde, is beoordeeld als zonder vleermuisroep, ofwel omdat het geen echolocatie-activiteit van vleermuizen toont (hoogste ultrasone CV: %.3f, drempel: %.2f), ofwel omdat het overeenkomt met een door de gebruiker gemarkeerd ruisvoorbeeld (laagste gelijkenis: %.3f, drempel: %.2f). Vleermuisroepen elders in de opname zijn niet als deze soort geïdentificeerd.",
	"pl": "Walidacja ultradźwiękowa i na podstawie przykładów szumu: każde okno audio, które dało to wykrycie, zostało uznane za niezawierające głosu nietoperza, albo dlatego że nie wykazuje echolokacji nietoperzy (najwyższe ultradźwiękowe CV: %.3f, próg: %.2f), albo dlatego że pasuje do przykładu szumu oznaczonego przez użytkownika (najniższe podobieństwo: %.3f, próg: %.2f). Odgłosy nietoperzy w innych miejscach nagrania nie zostały zidentyfikowane jako ten gatunek.",
	"pt": "Validação ultrassônica e por exemplos de ruído: todas as janelas de áudio que produziram esta detecção foram julgadas sem chamado de morcego, seja porque não mostram atividade de ecolocalização de morcegos (CV ultrassônico mais alto: %.3f, limiar: %.2f), seja porque correspondem a um exemplo de ruído marcado pelo usuário (menor similaridade: %.3f, limiar: %.2f). Chamados de morcegos em outras partes da gravação não foram identificados como esta espécie.",
	"sk": "Ultrazvuková validácia a validácia podľa príkladov šumu: každé zvukové okno, ktoré vytvorilo túto detekciu, bolo posúdené ako okno bez volania netopiera, buď preto, že nevykazuje echolokačnú aktivitu netopierov (najvyššie ultrazvukové CV: %.3f, prah: %.2f), alebo preto, že zodpovedá príkladu šumu označenému používateľom (najnižšia podobnosť: %.3f, prah: %.2f). Volania netopierov inde v zázname neboli identifikované ako tento druh.",
	"sv": "Ultraljuds- och brusexempelvalidering: varje ljudfönster som gav denna detektion bedömdes sakna fladdermusläte, antingen för att det inte visar någon ekolokaliseringsaktivitet från fladdermöss (högsta ultraljuds-CV: %.3f, tröskel: %.2f) eller för att det matchar ett brusexempel som användaren har markerat (lägsta likhet: %.3f, tröskel: %.2f). Fladdermusläten på andra ställen i inspelningen identifierades inte som denna art.",
}

// defaultUnlikelyCommentLocale is the fallback locale when the configured
// dashboard locale has no translation.
const defaultUnlikelyCommentLocale = "en"

// formatUnlikelyComment returns a localized comment explaining why a detection
// was tagged as unlikely. The template follows result.UnlikelySources: the noise
// example gate alone, both validators, or the ultrasonic CV filter, which is also
// the text for a result that names no source.
func formatUnlikelyComment(locale string, result *detection.Result) string {
	cv := result.UnlikelySources.Has(detection.UnlikelySourceUltrasonicCV)
	noise := result.UnlikelySources.Has(detection.UnlikelySourceNoiseExamples)
	switch {
	case cv && noise:
		return fmt.Sprintf(localizedTemplate(unlikelyCombinedCommentTemplates, locale),
			result.UltrasonicCV, result.UltrasonicCVThreshold, result.NoiseSimilarity, noisebank.MatchSimilarity)
	case noise:
		return fmt.Sprintf(localizedTemplate(unlikelyNoiseCommentTemplates, locale),
			result.NoiseSimilarity, noisebank.MatchSimilarity)
	default:
		return fmt.Sprintf(localizedTemplate(unlikelyCommentTemplates, locale),
			result.UltrasonicCV, result.UltrasonicCVThreshold)
	}
}

// localizedTemplate returns the template of locale, or the default locale's.
func localizedTemplate(templates map[string]string, locale string) string {
	if tmpl, ok := templates[locale]; ok {
		return tmpl
	}
	return templates[defaultUnlikelyCommentLocale]
}
