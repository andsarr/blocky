package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/0xERR0R/blocky/config/migration"
	"github.com/0xERR0R/blocky/log"
	"github.com/0xERR0R/blocky/util"
	"github.com/sirupsen/logrus"
)

type Schedule struct {
	Days   []time.Weekday
	Start  time.Time
	End    time.Time
	AllDay bool
}

func UnmarshalDays(days []string) ([]time.Weekday, error) {
	if len(days) == 0 {
		return []time.Weekday{
			time.Sunday, time.Monday, time.Tuesday, time.Wednesday,
			time.Thursday, time.Friday, time.Saturday,
		}, nil
	}

	weekdays := make([]time.Weekday, 0, len(days))
	dayStringToWeekday := map[string]time.Weekday{
		"sunday":    time.Sunday,
		"monday":    time.Monday,
		"tuesday":   time.Tuesday,
		"wednesday": time.Wednesday,
		"thursday":  time.Thursday,
		"friday":    time.Friday,
		"saturday":  time.Saturday,
	}

	for _, d := range days {
		wd, ok := dayStringToWeekday[strings.ToLower(d)]
		if !ok {
			return nil, fmt.Errorf("schedule: invalid day: %s", d)
		}
		weekdays = append(weekdays, wd)
	}

	return weekdays, nil
}

func (s *Schedule) UnmarshalYAML(unmarshal func(any) error) error {
	type rawSchedule struct {
		Days  []string `yaml:"days,omitempty"`
		Start string   `yaml:"start"`
		End   string   `yaml:"end"`
	}
	var raw rawSchedule
	if err := unmarshal(&raw); err != nil {
		return err
	}

	// Validate and convert days
	var weekdays []time.Weekday
	weekdays, err := UnmarshalDays(raw.Days)
	if err != nil {
		return err
	}
	s.Days = weekdays

	// Validate start and end
	if raw.Start == "" && raw.End == "" {
		s.AllDay = true
	} else {
		const layout = "15:04"
		startTime, err := time.Parse(layout, raw.Start)
		if err != nil {
			return fmt.Errorf("schedule: invalid start time: %w", err)
		}

		endTime, err := time.Parse(layout, raw.End)
		if err != nil {
			return fmt.Errorf("schedule: invalid end time: %w", err)
		}

		if startTime.After(endTime) {
			return errors.New("schedule: start time must be before end time")
		}

		s.AllDay = (startTime.Hour() == 0 && startTime.Minute() == 0 && startTime.Second() == 0) &&
			(endTime.Hour() == 23 && endTime.Minute() == 59 && endTime.Second() == 59)
		s.Start = startTime
		s.End = endTime
	}

	return nil
}

func (s *Schedule) AlwaysActive() bool {
	return s == nil || (s.AllDay && len(s.Days) == 7)
}

func (s *Schedule) IsActive() bool {
	if s == nil {
		return true // No schedule means always active
	}

	now := util.StripDate(time.Now())
	currentDay := now.Weekday()

	// Check if today is in the schedule
	for _, day := range s.Days {
		if day == currentDay {
			if s.AllDay {
				return true
			}

			// Check time range
			if now.After(s.Start) && now.Before(s.End) {
				return true
			}
		}
	}

	return false
}

type BlockItem struct {
	Name     string    `yaml:"-"`
	Schedule *Schedule `yaml:"schedule,omitempty"`
}

func (b *BlockItem) UnmarshalYAML(unmarshal func(any) error) error {
	// Try as string
	var name string
	if err := unmarshal(&name); err == nil {
		b.Name = name
		b.Schedule = nil

		return nil
	}
	// Try as map
	var m map[string]struct {
		Schedule *Schedule `yaml:"schedule,omitempty"`
	}
	if err := unmarshal(&m); err != nil {
		return err
	}
	for k, v := range m {
		b.Name = k
		b.Schedule = v.Schedule

		break
	}

	return nil
}

// Blocking configuration for query blocking
type Blocking struct {
	Denylists         map[string][]BytesSource `yaml:"denylists"`
	Allowlists        map[string][]BytesSource `yaml:"allowlists"`
	ClientGroupsBlock map[string][]BlockItem   `yaml:"clientGroupsBlock"`
	BlockType         string                   `yaml:"blockType" default:"ZEROIP"`
	BlockTTL          Duration                 `yaml:"blockTTL" default:"6h"`
	Loading           SourceLoading            `yaml:"loading"`

	// Deprecated options
	Deprecated struct {
		BlackLists            *map[string][]BytesSource `yaml:"blackLists"`
		WhiteLists            *map[string][]BytesSource `yaml:"whiteLists"`
		DownloadTimeout       *Duration                 `yaml:"downloadTimeout"`
		DownloadAttempts      *uint                     `yaml:"downloadAttempts"`
		DownloadCooldown      *Duration                 `yaml:"downloadCooldown"`
		RefreshPeriod         *Duration                 `yaml:"refreshPeriod"`
		FailStartOnListError  *bool                     `yaml:"failStartOnListError"`
		ProcessingConcurrency *uint                     `yaml:"processingConcurrency"`
		StartStrategy         *InitStrategy             `yaml:"startStrategy"`
		MaxErrorsPerFile      *int                      `yaml:"maxErrorsPerFile"`
	} `yaml:",inline"`
}

func (c *Blocking) migrate(logger *logrus.Entry) bool {
	return Migrate(logger, "blocking", c.Deprecated, map[string]Migrator{
		"blackLists":       Move(To("denylists", c)),
		"whiteLists":       Move(To("allowlists", c)),
		"downloadTimeout":  Move(To("loading.downloads.timeout", &c.Loading.Downloads)),
		"downloadAttempts": Move(To("loading.downloads.attempts", &c.Loading.Downloads)),
		"downloadCooldown": Move(To("loading.downloads.cooldown", &c.Loading.Downloads)),
		"refreshPeriod":    Move(To("loading.refreshPeriod", &c.Loading)),
		"failStartOnListError": Apply(To("loading.strategy", &c.Loading.Init), func(oldValue bool) {
			if oldValue {
				c.Loading.Strategy = InitStrategyFailOnError
			}
		}),
		"processingConcurrency": Move(To("loading.concurrency", &c.Loading)),
		"startStrategy":         Move(To("loading.strategy", &c.Loading.Init)),
		"maxErrorsPerFile":      Move(To("loading.maxErrorsPerSource", &c.Loading)),
	})
}

// IsEnabled implements `config.Configurable`.
func (c *Blocking) IsEnabled() bool {
	return len(c.ClientGroupsBlock) != 0
}

// LogConfig implements `config.Configurable`.
func (c *Blocking) LogConfig(logger *logrus.Entry) {
	logger.Info("clientGroupsBlock:")

	for key, val := range c.ClientGroupsBlock {
		logger.Infof("  %s = %v", key, val)
	}

	logger.Infof("blockType = %s", c.BlockType)

	if c.BlockType != "NXDOMAIN" {
		logger.Infof("blockTTL = %s", c.BlockTTL)
	}

	logger.Info("loading:")
	log.WithIndent(logger, "  ", c.Loading.LogConfig)

	logger.Info("denylists:")
	log.WithIndent(logger, "  ", func(logger *logrus.Entry) {
		c.logListGroups(logger, c.Denylists)
	})

	logger.Info("allowlists:")
	log.WithIndent(logger, "  ", func(logger *logrus.Entry) {
		c.logListGroups(logger, c.Allowlists)
	})
}

func (c *Blocking) logListGroups(logger *logrus.Entry, listGroups map[string][]BytesSource) {
	for group, sources := range listGroups {
		logger.Infof("%s:", group)

		for _, source := range sources {
			logger.Infof("   - %s", source)
		}
	}
}
