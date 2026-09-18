export interface Comment {
  id: number;
  entry: string;
  createdAt: string;
  updatedAt: string;
}

export interface SourceInfo {
  id: string;
  type?: string;
  displayName?: string;
}

/**
 * The time-of-day categories the backend classifier emits (see
 * `internal/suncalc.ClassifyTimeOfDay`) and that TimeOfDayIcon renders.
 * Detections carry `timeOfDay` as a plain string because the API models it as
 * one, so narrowing to this union is what the icon's prop expects.
 */
export type TimeOfDayValue = 'day' | 'night' | 'sunrise' | 'sunset' | 'dawn' | 'dusk';

export interface Detection {
  id: number;
  date: string;
  time: string;
  timestamp?: string; // ISO8601/RFC3339 with timezone from server
  source?: SourceInfo | null;
  beginTime: string;
  endTime: string;
  speciesCode: string;
  scientificName: string;
  commonName: string;
  confidence: number;
  modelType?: string; // AI model type (e.g. 'bird', 'bat'); drives the spectrogram frequency range
  verified: 'correct' | 'false_positive' | 'unverified';
  locked: boolean;
  unlikely?: boolean;
  comments?: Comment[];
  clipName?: string;
  weather?: Weather;
  timeOfDay?: string;
  // Species tracking metadata
  isNewSpecies?: boolean; // First seen within tracking window
  daysSinceFirstSeen?: number; // Days since species was first detected
  // Multi-period tracking metadata
  isNewThisYear?: boolean; // First time this year
  isNewThisSeason?: boolean; // First time this season
  daysThisYear?: number; // Days since first this year
  daysThisSeason?: number; // Days since first this season
  currentSeason?: string; // Current season name
}

export interface PaginatedDetectionResponse {
  data: Detection[];
  total: number;
  limit: number;
  offset: number;
  current_page: number;
  total_pages: number;
  // Additional fields for display
  showingFrom?: number;
  showingTo?: number;
  itemsPerPage?: number;
}

/**
 * Review verdict a detection can be filtered by. The empty string means the
 * filter is off; it is distinct from 'unverified', which selects detections that
 * carry no verdict.
 */
export type DetectionVerifiedFilter = '' | 'correct' | 'false_positive' | 'unverified';

/** Lock-state filter. The empty string means the filter is off. */
export type DetectionLockedFilter = '' | 'true' | 'false';

/**
 * Time-of-day period filter. These are resolved against the station's real
 * sunrise and sunset times, not fixed clock hours. The empty string means the
 * filter is off.
 */
export type DetectionTimeOfDayFilter = '' | 'day' | 'night' | 'sunrise' | 'sunset';

/**
 * The filter set shown in the detections filter panel and carried in the URL
 * query string, so a filtered view is shareable and survives reload and
 * back/forward navigation.
 *
 * Confidence is held as whole percentages (0-100) because that is what the range
 * inputs and the labels use; the API takes the same percentages and converts.
 */
export interface DetectionFilters {
  /** Free-text species query, matched against scientific and common names. */
  search: string;
  /** Inclusive start of the date range (YYYY-MM-DD). */
  startDate: string;
  /** Inclusive end of the date range (YYYY-MM-DD). */
  endDate: string;
  confidenceMin: number;
  confidenceMax: number;
  verified: DetectionVerifiedFilter;
  locked: DetectionLockedFilter;
  timeOfDay: DetectionTimeOfDayFilter;
  /**
   * Inclusive clock-hour band, each end a whole hour as a bare number string
   * ('0'-'23'); '' means that end is unbounded. This is the wall-clock companion
   * to timeOfDay, which follows the station's sun events instead. A dashboard
   * hourly drill-down arrives as `hour`/`duration` and is folded into this band,
   * so the hour it is filtering by is visible in the panel rather than applied
   * invisibly.
   */
  hourStart: string;
  hourEnd: string;
  /** Audio source, by display name. Empty means all sources. */
  source: string;
}

/** The filter values that mean "no constraint". */
export const DEFAULT_DETECTION_FILTERS: DetectionFilters = {
  search: '',
  startDate: '',
  endDate: '',
  confidenceMin: 0,
  confidenceMax: 100,
  verified: '',
  locked: '',
  timeOfDay: '',
  hourStart: '',
  hourEnd: '',
  source: '',
};

export interface DetectionsListData {
  notes: Detection[];
  queryType: 'hourly' | 'species' | 'search' | 'all';
  date: string;
  hour?: number;
  duration?: number;
  species?: string;
  search?: string;
  /**
   * The active filter set. Bulk "select all matching" resolves the same query the
   * user is looking at, so the list needs the filters to send to
   * /detections/batch/resolve -- without them the resolved set would be wider
   * than the visible one.
   */
  filters?: DetectionFilters;
  numResults: number;
  offset: number;
  totalResults: number;
  itemsPerPage: number;
  currentPage: number;
  totalPages: number;
  showingFrom: number;
  showingTo: number;
  dashboardSettings?: {
    thumbnails?: {
      summary?: boolean;
    };
  };
}

export type DetectionSortBy =
  | 'date_desc'
  | 'date_asc'
  | 'species_asc'
  | 'species_desc'
  | 'confidence_asc'
  | 'confidence_desc'
  | 'status';

export interface DetectionQueryParams {
  queryType?: 'hourly' | 'species' | 'search' | 'all';
  date?: string;
  hour?: string;
  duration?: number;
  species?: string;
  search?: string;
  start_date?: string;
  end_date?: string;
  numResults?: number;
  offset?: number;
  sortBy?: DetectionSortBy;
  // Advanced filters. Confidence bounds are whole percentages, matching the
  // filter panel's inputs; the backend converts them to fractions.
  confidenceMin?: number;
  confidenceMax?: number;
  verified?: DetectionVerifiedFilter;
  locked?: DetectionLockedFilter;
  timeOfDay?: DetectionTimeOfDayFilter;
  /** Clock-hour band as the API spells it: '7' for a single hour, '6-9' for a range. */
  hourRange?: string;
  source?: string;
}

export interface DetectionReviewRequest {
  comment?: string;
  verified?: 'correct' | 'false_positive';
  // Wire keys must match the /detections/:id/review request body the frontend
  // sends and the Go DetectionRequest binds (snake_case), otherwise the field
  // silently fails to bind server-side. See issue #3674. The frontend sends
  // `null` when the ignore checkbox is unchecked, so allow null here too.
  ignore_species?: string | null;
  lock_detection?: boolean;
}

export type ConfidenceLevel = 'high' | 'medium' | 'low';

export interface Weather {
  weatherIcon: string;
  description?: string;
  weatherMain?: string;
  temperature?: number;
  windSpeed?: number;
  windGust?: number;
  humidity?: number;
  units?: 'metric' | 'imperial' | 'standard';
  moonPhase?: number;
  moonPhaseName?: string;
  moonIllumination?: number;
}

export interface LatestWeatherResponse {
  daily?: {
    date: string;
    sunrise: string;
    sunset: string;
    country?: string;
    city_name?: string;
  };
  hourly?: {
    time: string;
    temperature: number;
    feels_like: number;
    temp_min?: number;
    temp_max?: number;
    pressure?: number;
    humidity?: number;
    visibility?: number;
    wind_speed?: number;
    wind_deg?: number;
    wind_gust?: number;
    clouds?: number;
    weather_main?: string;
    weather_desc?: string;
    weather_icon?: string;
  };
  moon?: {
    phase: number;
    phase_name: string;
    illumination: number;
    icon_name: string;
  };
  timestamp: string;
}

export interface TimeOfDayResponse {
  timeOfDay: string;
}

export interface ImageAttribution {
  authorName: string;
  authorURL: string;
  licenseName: string;
  licenseURL: string;
  sourceProvider: string;
}

export interface DailySpeciesSummary {
  scientific_name: string;
  common_name: string;
  species_code: string;
  count: number;
  hourly_counts: number[];
  high_confidence: boolean;
  max_confidence?: number; // Highest detection confidence for the day (fraction 0..1)
  first_heard: string;
  latest_heard: string;
  thumbnail_url: string;
  // Species tracking metadata
  is_new_species?: boolean; // True if first seen within tracking window (persistent from API)
  days_since_first_seen?: number; // Days since species was first detected
  days_since_last_seen?: number; // Days since the previous detection before this return (absence gap)
  // Multi-period tracking metadata
  is_new_this_year?: boolean; // First time this year
  is_new_this_season?: boolean; // First time this season
  days_this_year?: number; // Days since first this year
  days_this_season?: number; // Days since first this season
  current_season?: string; // Current season name
  // Animation state flags
  isNew?: boolean; // New species row animation (temporary for SSE updates)
  countIncreased?: boolean; // Count increment animation
  hourlyUpdated?: number[]; // Which hours were just updated
  previousCount?: number; // For animated counter
}
