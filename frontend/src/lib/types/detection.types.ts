export interface Comment {
  id: number;
  entry: string;
  createdAt: string;
  updatedAt: string;
}

export interface Detection {
  id: number;
  date: string;
  time: string;
  source: string;
  beginTime: string;
  endTime: string;
  speciesCode: string;
  scientificName: string;
  commonName: string;
  confidence: number;
  verified: 'correct' | 'false_positive' | 'unverified';
  locked: boolean;
  comments?: Comment[];
  clipName?: string;
  weather?: Weather;
  timeOfDay?: string;
  review?: {
    verified: 'correct' | 'false_positive' | 'unverified';
  };
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

export interface DetectionsListData {
  notes: Detection[];
  queryType: 'hourly' | 'species' | 'search' | 'all';
  date: string;
  hour?: number;
  duration?: number;
  species?: string;
  search?: string;
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
}

export interface DetectionReviewRequest {
  comment?: string;
  verified?: 'correct' | 'false_positive';
  ignoreSpecies?: string;
  locked?: boolean;
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
}

export interface TimeOfDayResponse {
  timeOfDay: string;
}

export interface DailySpeciesSummary {
  scientific_name: string;
  common_name: string;
  species_code: string;
  count: number;
  hourly_counts: number[];
  high_confidence: boolean;
  first_heard: string;
  latest_heard: string;
  thumbnail_url: string;
  // Animation state flags
  isNew?: boolean; // New species row animation
  countIncreased?: boolean; // Count increment animation
  hourlyUpdated?: number[]; // Which hours were just updated
  previousCount?: number; // For animated counter
}
