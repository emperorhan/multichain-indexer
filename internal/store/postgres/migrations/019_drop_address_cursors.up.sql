-- Migration 019: Drop address_cursors table.
-- All chains now use block-scan mode with pipeline_watermarks.
-- Per-address cursor tracking is no longer used.

DROP TABLE IF EXISTS address_cursors;
