package httpapi

import (
	"context"
	"log"
	"net/http"
	"time"

	"wikiatlas/backend/internal/domain"
	"wikiatlas/backend/internal/library"
)

// --- 媒体库：后台低频扫描 ---
//
// 扫描是确定性的（零 LLM 成本）：把三个源的条目清单 + 挂链状态写成快照，
// 建议池与 Altas 的库工具都读这份清单——页面秒开、不重复轰库。
// 频率在设置里可配（分钟，0 = 关闭）；忽略标记与"首次入库时间"跨扫描保留。

// libraryInventory 汇总三个源的全部条目（含已挂链的），供扫描落清单。
func (s *Server) libraryInventory(ctx context.Context) []domain.LibraryManifestEntry {
	out := make([]domain.LibraryManifestEntry, 0, 256)
	add := func(e domain.LibraryManifestEntry) { out = append(out, e) }

	// GameAtlas：按 GA 系列分容器；无系列的散游戏单列一格。
	if ga, err := s.gaClient(); err == nil {
		if entries, err := s.gaCatalogEntries(ctx, ga); err == nil {
			linked := s.linkedIDsBySource(domain.LibraryGameAtlas)
			for _, e := range entries {
				containerKey, containerTitle, extra := "series:none", "（无系列）", ""
				if e.Series != nil {
					containerKey = "series:" + e.Series.Name
					containerTitle = e.Series.Name
					extra = "GA系列：" + e.Series.Name
				}
				cover := ""
				if abs := absCover(ga.BaseURL(), e.CoverImage); abs != nil {
					cover = *abs
				}
				add(domain.LibraryManifestEntry{
					Key: "gameatlas:" + e.PublicID, Source: "gameatlas", EntryID: e.PublicID,
					Title: e.Title, TitleAlt: e.TitleAlt, ReleaseDate: e.ReleaseDate,
					CoverImage: cover, URL: ga.GameURL(e.PublicID), Kind: "game", Extra: extra,
					ContainerKey: containerKey, ContainerTitle: containerTitle, ContainerKind: "series",
					LinkedWorkID: linked[e.PublicID],
				})
			}
		} else {
			log.Printf("library scan: gameatlas: %v", err)
		}
	}

	// Emby：正片库 + 混杂库都进清单（混杂库里整组忽略是常规操作），容器 = 库名。
	if em, err := s.embyClient(); err == nil {
		if workViews, mixedViews, err := s.embyRoleViews(ctx, em); err == nil {
			type viewWithKind struct {
				v    library.EmbyView
				kind string
			}
			views := make([]viewWithKind, 0, len(workViews)+len(mixedViews))
			for _, v := range workViews {
				views = append(views, viewWithKind{v, "library"})
			}
			for _, v := range mixedViews {
				views = append(views, viewWithKind{v, "mixed"})
			}
			linked := s.linkedIDsBySource(domain.LibraryEmby)
			for _, vk := range views {
				items, err := em.ListItems(ctx, vk.v.ID, "Series,Movie,BoxSet")
				if err != nil {
					log.Printf("library scan: emby %s: %v", vk.v.Name, err)
					continue
				}
				for i := range items {
					it := items[i]
					var kind, extra string
					switch it.Type {
					case "Series":
						kind = "tv"
					case "Movie":
						kind = "movie"
					case "BoxSet":
						kind, extra = "collection", "合集"
					default:
						continue
					}
					sug := embySuggestionOf(em, &it, kind)
					cover := ""
					if sug.CoverImage != nil {
						cover = *sug.CoverImage
					}
					add(domain.LibraryManifestEntry{
						Key: "emby:" + it.ID, Source: "emby", EntryID: it.ID,
						Title: it.Name, TitleAlt: it.OriginalTitle, ReleaseDate: sug.ReleaseDate,
						CoverImage: cover, URL: sug.URL, Kind: kind, Extra: extra,
						ContainerKey: "view:" + vk.v.ID, ContainerTitle: vk.v.Name, ContainerKind: vk.kind,
						LinkedWorkID: linked[it.ID],
					})
				}
			}
		} else {
			log.Printf("library scan: emby views: %v", err)
		}
	}

	// Komga：条目集（正常系列 + 抽屉单册），容器 = 系列名（他们部署里每系列一"库"）。
	if km, err := s.komgaClient(); err == nil {
		if entries, err := s.komgaEntries(ctx, km); err == nil {
			linked := s.linkedIDsBySource(domain.LibraryKomga)
			for _, e := range entries {
				extra := ""
				if e.IsBook {
					extra = "抽屉系列拆出的单册"
				}
				add(domain.LibraryManifestEntry{
					Key: "komga:" + e.ID, Source: "komga", EntryID: e.ID,
					Title: e.Name, CoverImage: s.komgaCoverPath(e), URL: komgaEntryURL(km, e),
					Kind: "book", Format: e.formatStr(), Extra: extra,
					ContainerKey: "series:" + e.SeriesID, ContainerTitle: e.SeriesName, ContainerKind: "series",
					LinkedWorkID: linked[e.ID],
				})
			}
		} else {
			log.Printf("library scan: komga: %v", err)
		}
	}

	return out
}

// runLibraryScan：取全量 → 合并上一版清单（保留首见时间与忽略标记）→ 落库。
func (s *Server) runLibraryScan(ctx context.Context) (*domain.LibraryManifest, error) {
	// Komga 的系列层级先判一遍（LLM；只判新系列/书单变了的，常态零成本）——
	// 判完目录缓存会作废，下面的 inventory 才会按新层级出条目。
	if km, err := s.komgaClient(); err == nil {
		s.refreshKomgaJudgments(ctx, km)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	manifest := &domain.LibraryManifest{ScannedAt: now, Entries: s.libraryInventory(ctx)}
	if old, err := s.store.GetLibraryManifest(); err == nil && old != nil {
		manifest.PreviousScannedAt = old.ScannedAt
		manifest.IgnoredKeys = old.IgnoredKeys
		manifest.IgnoredGroups = old.IgnoredGroups
		firstSeen := make(map[string]string, len(old.Entries))
		for _, e := range old.Entries {
			firstSeen[e.Key] = e.FirstSeenAt
		}
		for i := range manifest.Entries {
			if t, ok := firstSeen[manifest.Entries[i].Key]; ok {
				manifest.Entries[i].FirstSeenAt = t
			} else {
				manifest.Entries[i].FirstSeenAt = now
			}
		}
	} else {
		for i := range manifest.Entries {
			manifest.Entries[i].FirstSeenAt = now
		}
	}
	if err := s.store.SaveLibraryManifest(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// scanIntervalMinutes：设置里的扫描间隔；未设 = 默认 360（6 小时）。
func scanIntervalMinutes(st *domain.Settings) int {
	if st.Library.ScanIntervalMinutes != nil {
		return *st.Library.ScanIntervalMinutes
	}
	return 360
}

// handleLibraryScan 是 POST /api/library/scan：手动扫一次（池子页「扫描媒体库」）。
func (s *Server) handleLibraryScan(w http.ResponseWriter, r *http.Request) {
	if !s.scanMu.TryLock() {
		writeErr(w, http.StatusConflict, "busy", "扫描正在进行中，稍等")
		return
	}
	defer s.scanMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	manifest, err := s.runLibraryScan(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "scan", err.Error())
		return
	}
	linked, unlinked := 0, 0
	for _, e := range manifest.Entries {
		if e.LinkedWorkID != "" {
			linked++
		} else {
			unlinked++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "scannedAt": manifest.ScannedAt,
		"entries": len(manifest.Entries), "linked": linked, "unlinked": unlinked,
	})
}

// Start 起后台循环（main 里调一次）；Stop 收掉。
func (s *Server) Start() {
	s.scanStopCh = make(chan struct{})
	go s.libraryScanLoop()
}

func (s *Server) Stop() {
	if s.scanStopCh != nil {
		close(s.scanStopCh)
		s.scanStopCh = nil
	}
}

func (s *Server) libraryScanLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.scanStopCh:
			return
		case <-t.C:
			s.maybeLibraryScan()
		}
	}
}

// maybeLibraryScan：到点才扫。间隔 0 = 关闭；库一个都没配就不扫。
func (s *Server) maybeLibraryScan() {
	st, err := s.store.GetSettings()
	if err != nil {
		return
	}
	interval := scanIntervalMinutes(st)
	if interval <= 0 {
		return
	}
	if st.Library.EmbyURL == nil && st.Library.KomgaURL == nil && st.Library.GameAtlasURL == nil {
		return
	}
	if m, err := s.store.GetLibraryManifest(); err == nil && m != nil {
		if t, err := time.Parse(time.RFC3339, m.ScannedAt); err == nil && time.Since(t) < time.Duration(interval)*time.Minute {
			return
		}
	}
	if !s.scanMu.TryLock() {
		return
	}
	defer s.scanMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := s.runLibraryScan(ctx); err != nil {
		log.Printf("library scan (auto): %v", err)
	}
}
