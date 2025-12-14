<!--
  FlagIcon Component

  Purpose: Renders country flag icons based on locale code.
  This component provides a type-safe way to display flag icons
  without using raw HTML injection.

  Props:
  - locale: The locale code (en, de, fr, etc.) or BirdNET locale code
  - className: Optional CSS classes for sizing/styling

  @component
-->
<script lang="ts">
  // Import all flag icons as raw SVG strings
  // UI language flags
  import GbFlag from '$lib/assets/icons/flags/gb.svg?raw';
  import DeFlag from '$lib/assets/icons/flags/de.svg?raw';
  import FrFlag from '$lib/assets/icons/flags/fr.svg?raw';
  import EsFlag from '$lib/assets/icons/flags/es.svg?raw';
  import FiFlag from '$lib/assets/icons/flags/fi.svg?raw';
  import NlFlag from '$lib/assets/icons/flags/nl.svg?raw';
  import PlFlag from '$lib/assets/icons/flags/pl.svg?raw';
  import PtFlag from '$lib/assets/icons/flags/pt.svg?raw';

  // Additional BirdNET locale flags
  import ZaFlag from '$lib/assets/icons/flags/za.svg?raw';
  import SaFlag from '$lib/assets/icons/flags/sa.svg?raw';
  import BgFlag from '$lib/assets/icons/flags/bg.svg?raw';
  import CzFlag from '$lib/assets/icons/flags/cz.svg?raw';
  import CnFlag from '$lib/assets/icons/flags/cn.svg?raw';
  import HrFlag from '$lib/assets/icons/flags/hr.svg?raw';
  import DkFlag from '$lib/assets/icons/flags/dk.svg?raw';
  import GrFlag from '$lib/assets/icons/flags/gr.svg?raw';
  import UsFlag from '$lib/assets/icons/flags/us.svg?raw';
  import EeFlag from '$lib/assets/icons/flags/ee.svg?raw';
  import IlFlag from '$lib/assets/icons/flags/il.svg?raw';
  import InFlag from '$lib/assets/icons/flags/in.svg?raw';
  import HuFlag from '$lib/assets/icons/flags/hu.svg?raw';
  import IsFlag from '$lib/assets/icons/flags/is.svg?raw';
  import IdFlag from '$lib/assets/icons/flags/id.svg?raw';
  import ItFlag from '$lib/assets/icons/flags/it.svg?raw';
  import JpFlag from '$lib/assets/icons/flags/jp.svg?raw';
  import KrFlag from '$lib/assets/icons/flags/kr.svg?raw';
  import LvFlag from '$lib/assets/icons/flags/lv.svg?raw';
  import LtFlag from '$lib/assets/icons/flags/lt.svg?raw';
  import NoFlag from '$lib/assets/icons/flags/no.svg?raw';
  import BrFlag from '$lib/assets/icons/flags/br.svg?raw';
  import RoFlag from '$lib/assets/icons/flags/ro.svg?raw';
  import RuFlag from '$lib/assets/icons/flags/ru.svg?raw';
  import RsFlag from '$lib/assets/icons/flags/rs.svg?raw';
  import SkFlag from '$lib/assets/icons/flags/sk.svg?raw';
  import SiFlag from '$lib/assets/icons/flags/si.svg?raw';
  import SeFlag from '$lib/assets/icons/flags/se.svg?raw';
  import ThFlag from '$lib/assets/icons/flags/th.svg?raw';
  import TrFlag from '$lib/assets/icons/flags/tr.svg?raw';
  import UaFlag from '$lib/assets/icons/flags/ua.svg?raw';
  import VnFlag from '$lib/assets/icons/flags/vn.svg?raw';

  // Locale type definition - includes both UI locales and BirdNET locale codes
  export type FlagLocale =
    // UI language locales
    | 'en'
    | 'de'
    | 'fr'
    | 'es'
    | 'fi'
    | 'nl'
    | 'pl'
    | 'pt'
    // BirdNET locale codes
    | 'af'
    | 'ar'
    | 'bg'
    | 'ca'
    | 'cs'
    | 'da'
    | 'el'
    | 'en-uk'
    | 'en-us'
    | 'et'
    | 'he'
    | 'hi-in'
    | 'hr'
    | 'hu'
    | 'id'
    | 'is'
    | 'it'
    | 'ja'
    | 'ko'
    | 'lt'
    | 'lv'
    | 'ml'
    | 'no'
    | 'pt-br'
    | 'pt-pt'
    | 'ro'
    | 'ru'
    | 'sk'
    | 'sl'
    | 'sr'
    | 'sv'
    | 'th'
    | 'tr'
    | 'uk'
    | 'vi-vn'
    | 'zh';

  interface Props {
    locale: FlagLocale;
    className?: string;
  }

  let { locale, className = 'size-5' }: Props = $props();

  // Map locale codes to their SVG content
  const flagIcons: Record<FlagLocale, string> = {
    // UI language locales
    en: GbFlag,
    de: DeFlag,
    fr: FrFlag,
    es: EsFlag,
    fi: FiFlag,
    nl: NlFlag,
    pl: PlFlag,
    pt: PtFlag,
    // BirdNET locale codes mapped to country flags
    af: ZaFlag, // Afrikaans -> South Africa
    ar: SaFlag, // Arabic -> Saudi Arabia
    bg: BgFlag, // Bulgarian -> Bulgaria
    ca: EsFlag, // Catalan -> Spain (Catalonia is part of Spain)
    cs: CzFlag, // Czech -> Czech Republic
    da: DkFlag, // Danish -> Denmark
    el: GrFlag, // Greek -> Greece
    'en-uk': GbFlag, // English UK -> Great Britain
    'en-us': UsFlag, // English US -> United States
    et: EeFlag, // Estonian -> Estonia
    he: IlFlag, // Hebrew -> Israel
    'hi-in': InFlag, // Hindi -> India
    hr: HrFlag, // Croatian -> Croatia
    hu: HuFlag, // Hungarian -> Hungary
    id: IdFlag, // Indonesian -> Indonesia
    is: IsFlag, // Icelandic -> Iceland
    it: ItFlag, // Italian -> Italy
    ja: JpFlag, // Japanese -> Japan
    ko: KrFlag, // Korean -> South Korea
    lt: LtFlag, // Lithuanian -> Lithuania
    lv: LvFlag, // Latvian -> Latvia
    ml: InFlag, // Malayalam -> India
    no: NoFlag, // Norwegian -> Norway
    'pt-br': BrFlag, // Brazilian Portuguese -> Brazil
    'pt-pt': PtFlag, // Portuguese Portugal -> Portugal
    ro: RoFlag, // Romanian -> Romania
    ru: RuFlag, // Russian -> Russia
    sk: SkFlag, // Slovak -> Slovakia
    sl: SiFlag, // Slovenian -> Slovenia
    sr: RsFlag, // Serbian -> Serbia
    sv: SeFlag, // Swedish -> Sweden
    th: ThFlag, // Thai -> Thailand
    tr: TrFlag, // Turkish -> Turkey
    uk: UaFlag, // Ukrainian -> Ukraine
    'vi-vn': VnFlag, // Vietnamese -> Vietnam
    zh: CnFlag, // Chinese -> China
  };

  // Get the icon for the current locale
  let iconSvg = $derived(flagIcons[locale] || GbFlag);
</script>

<!--
  Note: We use {@html} here because the SVG icons are static assets
  imported at build time, not user-generated content. The icons are
  trusted and sanitized by the build process.
-->
<span class="{className} shrink-0 [&>svg]:size-full [&>svg]:block">
  {@html iconSvg}
</span>
