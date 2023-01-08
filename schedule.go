package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	defaultRate int64
	blocks      []ScheduleBlock
}

type ScheduleBlock struct {
	weekday     time.Weekday
	startHour   int
	startMinute int
	endHour     int
	endMinute   int
	rate        int64
}

func parseWeekday(s string) (time.Weekday, error) {
	switch s {
	case "mon", "monday":
		return time.Monday, nil
	case "tue", "tuesday":
		return time.Tuesday, nil
	case "wed", "wednesday":
		return time.Wednesday, nil
	case "thu", "thursday":
		return time.Thursday, nil
	case "fri", "friday":
		return time.Friday, nil
	case "sat", "saturday":
		return time.Saturday, nil
	case "sun", "sunday":
		return time.Sunday, nil
	default:
		return -1, fmt.Errorf("invalid week day: %s", s)
	}
}

func readSchedule(fn string) (*Schedule, error) {
	file, err := os.Open(fn)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var defaultRate int64
	var blocks []ScheduleBlock
	scanner := bufio.NewScanner(file)
	lineNo := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineNo++

		if len(line) == 0 || line[0] == '#' {
			continue
		}

		if strings.HasPrefix(line, "default:") {
			parts := strings.SplitN(line, ":", 2)
			defaultRate, err = parseRate(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, err
			}

			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid format on line %d (expected one colon)", lineNo)
		}

		temporalSpec := strings.Split(strings.TrimSpace(parts[0]), " ")
		if len(temporalSpec) != 2 {
			return nil, fmt.Errorf("invalid format on line %d (missing weekday or time spec)", lineNo)
		}

		weekdaySpec := strings.Split(temporalSpec[0], "-")
		if len(weekdaySpec) > 2 {
			return nil, fmt.Errorf("invalid format on line %d (too many '-' characters)", lineNo)
		}

		startWeekday, err := parseWeekday(weekdaySpec[0])
		if err != nil {
			return nil, err
		}

		weekdays := []time.Weekday{startWeekday}
		if len(weekdaySpec) > 1 {
			endWeekday, err := parseWeekday(weekdaySpec[1])
			if err != nil {
				return nil, err
			}
			if endWeekday < startWeekday {
				endWeekday += 7
			}
			for i := startWeekday + 1; i <= endWeekday; i++ {
				weekdays = append(weekdays, i%7)
			}
		}

		timeRange := strings.Split(temporalSpec[1], "-")
		if len(timeRange) != 2 {
			return nil, fmt.Errorf("invalid format on line %d (bad time range)", lineNo)
		}
		if len(timeRange[0]) != 4 || len(timeRange[1]) != 4 {
			return nil, fmt.Errorf("invalid format on line %d (bad time range). missing leading zero?", lineNo)
		}
		startHour, err := strconv.Atoi(timeRange[0][0:2])
		if err != nil {
			return nil, err
		}
		startMinute, err := strconv.Atoi(timeRange[0][2:4])
		if err != nil {
			return nil, err
		}
		endHour, err := strconv.Atoi(timeRange[1][0:2])
		if err != nil {
			return nil, err
		}
		endMinute, err := strconv.Atoi(timeRange[1][2:4])
		if err != nil {
			return nil, err
		}
		if startHour > 23 || startMinute > 59 ||
			endHour > 23 || endMinute > 59 ||
			endHour < startHour ||
			(startHour == endHour && endMinute < startMinute) {
			return nil, fmt.Errorf("invalid format on line %d (bad time spec)", lineNo)
		}

		rate, err := parseRate(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, err
		}

		for _, weekday := range weekdays {
			blocks = append(blocks, ScheduleBlock{weekday, startHour, startMinute, endHour, endMinute, rate})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].weekday < blocks[j].weekday ||
			(blocks[i].weekday == blocks[j].weekday &&
				blocks[i].startHour < blocks[j].startHour) ||
			(blocks[i].weekday == blocks[j].weekday &&
				blocks[i].startHour == blocks[j].startHour &&
				blocks[i].startMinute < blocks[j].startMinute)
	})

	if len(blocks) == 0 {
		return nil, errors.New("schedule is empty")
	} else if len(blocks) > 1 {
		for i := 0; i < len(blocks)-1; i++ {
			j := (i + 1)
			if blocks[i].weekday != blocks[j].weekday {
				continue
			}
			if blocks[i].endHour > blocks[j].startHour ||
				(blocks[i].endHour == blocks[j].startHour && blocks[i].endMinute > blocks[j].startMinute) {
				return nil, errors.New("time ranges are not allowed to overlap")
			}
		}
	}

	return &Schedule{defaultRate, blocks}, nil
}

func (s Schedule) next() ScheduleBlock {
	now := time.Now()
	var minBlock *ScheduleBlock
	var minTimeUntil time.Duration
	for i := range s.blocks {
		block := s.blocks[i]
		start, _ := block.next()
		timeUntil := start.Sub(now)
		if minBlock == nil || timeUntil < 0 || timeUntil < minTimeUntil {
			minBlock = &block
			minTimeUntil = timeUntil
		}
	}
	return *minBlock
}

func (block ScheduleBlock) next() (time.Time, time.Time) {
	now := time.Now()
	today := now.Weekday()
	days := int(block.weekday-today+7) % 7
	t := now.AddDate(0, 0, days)
	start := time.Date(t.Year(), t.Month(), t.Day(), block.startHour, block.startMinute, 0, 0, t.Location())
	end := time.Date(t.Year(), t.Month(), t.Day(), block.endHour, block.endMinute, 0, 0, t.Location())

	// Add a week if the time has already passed this week
	// This also accounts for DST (the actual time may be different after constructing the time object) 😱
	if end.Before(start) {
		end = end.Add(time.Hour)
	}
	if now.After(end) {
		t = t.AddDate(0, 0, 7)
		start = time.Date(t.Year(), t.Month(), t.Day(), block.startHour, block.startMinute, 0, 0, t.Location())
		end = time.Date(t.Year(), t.Month(), t.Day(), block.endHour, block.endMinute, 0, 0, t.Location())
	}

	return start, end
}

func (block ScheduleBlock) active() bool {
	nextStart, _ := block.next()
	now := time.Now()
	return now.After(nextStart)
}
