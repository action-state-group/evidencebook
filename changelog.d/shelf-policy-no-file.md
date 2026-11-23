### Fixed: a refused window policy leaves no shelf file (#827)

- `OpenSQLiteShelf` checks a window policy before it touches the disk, so a refused policy leaves no shelf or lock file behind (evidencebook#827).
