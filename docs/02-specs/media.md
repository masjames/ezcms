# Media Spec

## Purpose

- Define the upload-time media processing and reader-time delivery contract.

## Rules

- Media ingestion MUST occur through a single multipart upload request.
- The media pipeline MUST preprocess images during upload, not on first reader request.
- The upload pass MUST generate all required variants in one operation.
- Required variants are:
- `hero`: max width 1600px
- `og`: width 1200px
- `thumb`: width 400px
- All generated variants MUST be converted to WebP at quality 75.
- EXIF metadata MUST be stripped during preprocessing.
- Generated variants MUST be stored in object storage.
- The database MUST store canonical CDN URLs for each variant, not raw object storage paths.
- Public HTML MUST reference the stored CDN URLs directly.
- Reader image delivery MUST NOT require a backend hop through Render or Next.js.
- Adding a new variant later MUST be treated as a schema and backfill operation, not an on-demand reader transform.

## Failure Modes

- Deferring variants to request time shifts cost and latency onto reader traffic.
- Storing file paths instead of CDN URLs couples delivery to storage naming and CDN configuration.
- Serving media through the backend breaks the zero-marginal-cost delivery model.
- Missing a required variant causes consuming surfaces to invent their own transform behavior.

## Observability

- Log upload processing steps, generated variants, and object storage write outcomes per media asset.
- Validate persisted media records contain all required variant URLs.
- Track upload failures by processing stage: decode, resize, convert, metadata strip, storage write, database write.
- Monitor CDN delivery success independently from backend request volume; reader image traffic should not correlate with backend load.
