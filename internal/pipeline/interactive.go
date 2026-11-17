package pipeline

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"animarr/internal/config"
	"animarr/internal/indexer"
	"animarr/internal/parser"
	"animarr/internal/score"
	"animarr/internal/store"
)

// Choice is one Nyaa release ranked for a person to grab.
// Rejected releases stay in the list so a manual grab can override the automatic rules.
type Choice struct {
	Release  indexer.Release
	Parsed   parser.Parsed
	Score    int
	Reasons  []string
	Rejected bool
	Reject   string
}

// ListChoices searches the configured indexer and ranks every result.
// episode 0 is the whole show, including batch and season packs.
// The call is a user action: AUTOMATION_ENABLED does not block it.
func (s *Service) ListChoices(ctx context.Context, showID int64, episode int) ([]Choice, error) {
	cfg := s.live(ctx)
	sh, err := s.Store.GetShow(ctx, showID)
	if err != nil {
		return nil, err
	}
	if episode > 0 {
		ep, err := s.Store.GetEpisode(ctx, showID, episode)
		if err != nil {
			return nil, err
		}
		if ep.State == store.StateIgnored {
			return nil, fmt.Errorf("episode is ignored")
		}
	}
	rels, err := s.searchTitles(ctx, cfg, sh)
	if err != nil {
		return nil, err
	}
	return rankChoices(cfg, sh, episode, rels, s.numberingFor(ctx, sh)), nil
}

// GrabChoice sends one release from the interactive list to qBittorrent.
// episode is the row the person clicked. 0 grabs a show-level result:
// a single release ties to its episode, and a batch ties to every episode in its range.
// The call is a user action: AUTOMATION_ENABLED does not block it.
func (s *Service) GrabChoice(ctx context.Context, showID int64, episode int, infoHash string) error {
	cfg := s.live(ctx)
	infoHash = strings.ToLower(strings.TrimSpace(infoHash))
	if infoHash == "" {
		return fmt.Errorf("release has no infohash")
	}
	sh, err := s.Store.GetShow(ctx, showID)
	if err != nil {
		return err
	}
	if episode > 0 {
		ep, err := s.Store.GetEpisode(ctx, showID, episode)
		if err != nil {
			return err
		}
		if ep.State == store.StateIgnored {
			return fmt.Errorf("episode is ignored")
		}
	}
	rels, err := s.searchTitles(ctx, cfg, sh)
	if err != nil {
		return err
	}
	var found *indexer.Release
	for i := range rels {
		if strings.EqualFold(rels[i].InfoHash, infoHash) {
			found = &rels[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("release is no longer in the search results")
	}
	parsed := parser.Parse(found.Title)
	rep := score.Explain(parsed, found.Seeders, sh.PreferredResolution, cfg.Score)
	eps, err := s.episodesForGrab(ctx, sh, episode, parsed, s.numberingFor(ctx, sh))
	if err != nil {
		return err
	}
	return s.grabEpisodes(ctx, cfg, sh, eps, picked{rel: *found, parsed: parsed, score: rep.Total}, true, true)
}

func (s *Service) episodesForGrab(ctx context.Context, sh store.Show, chosen int, p parser.Parsed, nb numbering) ([]store.Episode, error) {
	nums := map[int]struct{}{}
	if chosen > 0 {
		nums[chosen] = struct{}{}
	}
	if p.Batch && p.Episode > 0 && p.EpisodeEnd >= p.Episode {
		// A pack counted on from earlier seasons maps back to this season's numbers.
		shift := 0
		if nb.offset > 0 && p.Episode > nb.offset {
			shift = nb.offset
		}
		end := p.EpisodeEnd
		if end > p.Episode+200 {
			end = p.Episode + 200
		}
		for n := p.Episode; n <= end; n++ {
			nums[n-shift] = struct{}{}
		}
	} else if chosen == 0 {
		if strings.EqualFold(sh.Format, "MOVIE") {
			nums[1] = struct{}{}
		} else if local, _, ok := nb.local(sh, p.Episode); ok {
			nums[local] = struct{}{}
		}
	}
	if len(nums) == 0 {
		return nil, fmt.Errorf("this pack does not say which episodes it covers; grab it from an episode row")
	}
	list := make([]int, 0, len(nums))
	for n := range nums {
		list = append(list, n)
	}
	slices.Sort(list)
	var eps []store.Episode
	for _, n := range list {
		ep, err := s.Store.GetEpisode(ctx, sh.ID, n)
		if err != nil {
			if n == chosen {
				return nil, err
			}
			continue
		}
		if ep.State == store.StateIgnored && n != chosen {
			continue
		}
		eps = append(eps, ep)
	}
	if len(eps) == 0 {
		return nil, fmt.Errorf("no matching episodes for this release")
	}
	return eps, nil
}

func rankChoices(cfg config.Config, sh store.Show, episode int, rels []indexer.Release, nb numbering) []Choice {
	out := make([]Choice, 0, len(rels))
	for _, rel := range rels {
		p := parser.Parse(rel.Title)
		rep := score.Explain(p, rel.Seeders, sh.PreferredResolution, cfg.Score)
		ch := Choice{
			Release: rel,
			Parsed:  p,
			Score:   rep.Total,
			Reasons: append([]string(nil), rep.Lines...),
		}
		var why []string
		if !nb.titleOK(p, sh, cfg.MatchThreshold) {
			why = append(why, "title did not match")
		}
		if episode > 0 && !nb.coversEpisode(sh, p, episode) {
			why = append(why, episodeReject(p, episode))
		}
		if p.Batch {
			why = append(why, "batch; automatic search skips packs")
		}
		if rel.Seeders < cfg.Score.MinSeeders {
			why = append(why, fmt.Sprintf("%d seeders is below the floor of %d", rel.Seeders, cfg.Score.MinSeeders))
		}
		if len(why) > 0 {
			ch.Rejected = true
			ch.Reject = strings.Join(why, "; ")
		}
		out = append(out, ch)
	}
	slices.SortStableFunc(out, func(a, b Choice) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		if a.Release.Seeders != b.Release.Seeders {
			return b.Release.Seeders - a.Release.Seeders
		}
		return strings.Compare(a.Release.Title, b.Release.Title)
	})
	return out
}

func spanCovers(p parser.Parsed, episode int) bool {
	if p.Episode <= 0 {
		return false
	}
	end := p.EpisodeEnd
	if end < p.Episode {
		end = p.Episode
	}
	return episode >= p.Episode && episode <= end
}

func episodeReject(p parser.Parsed, episode int) string {
	if p.Batch {
		if p.Episode > 0 && p.EpisodeEnd >= p.Episode {
			return fmt.Sprintf("batch is E%02d–E%02d, not E%02d", p.Episode, p.EpisodeEnd, episode)
		}
		return fmt.Sprintf("batch does not list E%02d", episode)
	}
	if p.Episode <= 0 {
		return fmt.Sprintf("no episode number; this row is E%02d", episode)
	}
	return fmt.Sprintf("wrong episode (release is E%02d, this row is E%02d)", p.Episode, episode)
}
