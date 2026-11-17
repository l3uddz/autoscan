CREATE TABLE scan_target (
    id TEXT NOT NULL PRIMARY KEY,
    retry_delay INTEGER NOT NULL DEFAULT 0,
    next_retry INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE scan_delivery (
    target TEXT NOT NULL,
    folder TEXT NOT NULL,
    PRIMARY KEY (target, folder)
);

CREATE INDEX scan_delivery_folder ON scan_delivery (folder);
