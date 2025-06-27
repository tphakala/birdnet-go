# Image Provider Performance Issues

## Issues Discovered

1. **Rate limiting affects user-facing requests**
   - Wikipedia provider has 10 req/s rate limit
   - Each image fetch makes 2 API calls (thumbnail + author info)
   - Results in ~400-500ms per image fetch
   - Rate limiting was meant only for background cache refresh, not user requests

2. **Cache not being utilized properly**
   - DB cache lookups are happening but finding 0 results
   - Memory cache is working but gets cleared on restart
   - Need to investigate why DB cache entries aren't being found

3. **Potential Solutions**
   - Separate rate limiters for user requests vs background refresh
   - Implement true batch API calls to Wikipedia (if supported)
   - Fix DB cache lookup issues
   - Consider caching author info separately with longer TTL
   - Pre-populate cache on startup for common species

## Test Results
```
2025/06/27 12:51:31 GetBatch: Memory cache check completed in 5.327µs - found 2/20 in cache
2025/06/27 12:51:31 GetBatch: Batch DB lookup completed in 1.42101ms - found 0 more images, 18 still missing
2025/06/27 12:51:31 fetchAndStore: Provider fetch for Haemorhous mexicanus took 451.259605ms
```

## Branch for fixes
Create new branch: `fix/imageprovider-performance`