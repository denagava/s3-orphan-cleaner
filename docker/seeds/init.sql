CREATE TABLE IF NOT EXISTS public.assets (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       TEXT        NOT NULL,
    metadata   JSONB       NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);


INSERT INTO public.assets (kind, metadata) VALUES


    ('IMAGE', '{"image":{"renditions":{
        "small":      "uploads/img1_low.jpg",
        "original": "uploads/img1_orig.jpg",
        "medium":  "uploads/img1_prev.jpg"
    }}}'),

    ('IMAGE', '{"storageKey":"uploads/legacy_img.jpg"}'),


    ('IMAGE', '{"image":{"renditions":{"original":"uploads/img2_orig.jpg"}}}'),

    ('IMAGE', '{}'),

    ('DOCUMENT', '{"document":{
        "sourceKey": "documents/report.pdf",
        "pages": [
            {"key": "documents/page1.jpg",
             "renditions": {"small": "documents/page1_low.jpg"}},
            {"key": "documents/page2.jpg"}
        ]
    }}'),

    ('TEXT', '{"content":"Hello, s3-orphan-cleaner seed!"}');

