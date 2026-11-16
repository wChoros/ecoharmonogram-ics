package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Nieoficjalne API, z którego korzysta aplikacja mobilna EcoHarmonogram.
const apiURL = "https://ecoharmonogram.pl/api/api.php"

var httpClient = &http.Client{Timeout: 30 * time.Second}

type Town struct {
	ID          string `json:"id"`
	CommunityID string `json:"communityId"`
	Name        string `json:"name"`
	District    string `json:"district"`
	Province    string `json:"province"`
}

type Period struct {
	ID        string `json:"id"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}

type Street struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Sides string `json:"sides"`
}

type scheduleEntry struct {
	Month         string `json:"month"`
	Days          string `json:"days"`
	Year          string `json:"year"`
	DescriptionID string `json:"scheduleDescriptionId"`
}

type scheduleDescription struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Collection to jeden termin odbioru danej frakcji.
type Collection struct {
	Date time.Time
	Kind string
}

func apiGet(action string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("action", action)
	req, err := http.NewRequest(http.MethodGet, apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Dalvik/2.1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", action, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")) // API zwraca BOM
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return fmt.Errorf("%s: pusta odpowiedź (złe parametry?)", action)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: niepoprawny JSON: %w", action, err)
	}
	return nil
}

func getTowns() ([]Town, error) {
	var r struct {
		Towns []Town `json:"towns"`
	}
	return r.Towns, apiGet("getTowns", nil, &r)
}

func getPeriods(townID string) ([]Period, error) {
	var r struct {
		Periods []Period `json:"schedulePeriods"`
	}
	return r.Periods, apiGet("getSchedulePeriods", url.Values{"townId": {townID}}, &r)
}

func getStreets(townID, periodID string) ([]Street, error) {
	var r struct {
		Streets []Street `json:"streets"`
	}
	return r.Streets, apiGet("getStreets", url.Values{"townId": {townID}, "schedulePeriodId": {periodID}}, &r)
}

func getCollections(streetID, periodID string) ([]Collection, error) {
	var r struct {
		Schedules    []scheduleEntry       `json:"schedules"`
		Descriptions []scheduleDescription `json:"scheduleDescription"`
	}
	if err := apiGet("getSchedules", url.Values{"streetId": {streetID}, "schedulePeriodId": {periodID}}, &r); err != nil {
		return nil, err
	}
	names := make(map[string]string, len(r.Descriptions))
	for _, d := range r.Descriptions {
		names[d.ID] = strings.TrimSpace(d.Name)
	}
	var out []Collection
	for _, s := range r.Schedules {
		year, err1 := strconv.Atoi(s.Year)
		month, err2 := strconv.Atoi(s.Month)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("getSchedules: zła data %q-%q", s.Year, s.Month)
		}
		kind, ok := names[s.DescriptionID]
		if !ok {
			kind = "ODPADY"
		}
		for _, d := range strings.Split(s.Days, ";") {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			day, err := strconv.Atoi(d)
			if err != nil {
				return nil, fmt.Errorf("getSchedules: zły dzień %q", d)
			}
			date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
			if date.Day() != day { // np. 31 lutego
				return nil, fmt.Errorf("getSchedules: nieistniejąca data %d-%02d-%02d", year, month, day)
			}
			out = append(out, Collection{Date: date, Kind: kind})
		}
	}
	return out, nil
}

// findTown wybiera miejscowość po nazwie i (opcjonalnie) id gminy.
func findTown(towns []Town, name, communityID string) (Town, error) {
	var matches []Town
	for _, t := range towns {
		if strings.EqualFold(t.Name, name) && (communityID == "" || t.CommunityID == communityID) {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Town{}, fmt.Errorf("nie znaleziono miejscowości %q (gmina %q); użyj `ecocal lookup`", name, communityID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "miejscowość %q występuje kilka razy, podaj -community:\n", name)
	for _, t := range matches {
		fmt.Fprintf(&b, "  community=%s  gm. %s, woj. %s\n", t.CommunityID, t.District, t.Province)
	}
	return Town{}, fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

// findStreet wybiera ulicę po nazwie; pusty `name` pasuje do miejscowości bez ulic.
func findStreet(streets []Street, name string) (Street, error) {
	var matches []Street
	for _, s := range streets {
		if strings.EqualFold(strings.TrimSpace(s.Name), strings.TrimSpace(name)) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Street{}, fmt.Errorf("nie znaleziono ulicy %q; użyj `ecocal lookup`", name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ulica %q ma kilka harmonogramów (nieobsługiwane):\n", name)
	for _, s := range matches {
		fmt.Fprintf(&b, "  id=%s  %s\n", s.ID, s.Sides)
	}
	return Street{}, fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}
