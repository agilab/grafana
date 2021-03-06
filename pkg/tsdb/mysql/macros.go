package mysql

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/tsdb"
)

//const rsString = `(?:"([^"]*)")`;
const rsIdentifier = `([_a-zA-Z0-9]+)`
const sExpr = `\$` + rsIdentifier + `\(([^\)]*)\)`

type MySqlMacroEngine struct {
	TimeRange *tsdb.TimeRange
	Query     *tsdb.Query
}

func NewMysqlMacroEngine() tsdb.SqlMacroEngine {
	return &MySqlMacroEngine{}
}

func (m *MySqlMacroEngine) Interpolate(query *tsdb.Query, timeRange *tsdb.TimeRange, sql string) (string, error) {
	m.TimeRange = timeRange
	m.Query = query
	rExp, _ := regexp.Compile(sExpr)
	var macroError error

	sql = replaceAllStringSubmatchFunc(rExp, sql, func(groups []string) string {
		args := strings.Split(groups[2], ",")
		for i, arg := range args {
			args[i] = strings.Trim(arg, " ")
		}
		res, err := m.evaluateMacro(groups[1], args)
		if err != nil && macroError == nil {
			macroError = err
			return "macro_error()"
		}
		return res
	})

	if macroError != nil {
		return "", macroError
	}

	return sql, nil
}

func replaceAllStringSubmatchFunc(re *regexp.Regexp, str string, repl func([]string) string) string {
	result := ""
	lastIndex := 0

	for _, v := range re.FindAllSubmatchIndex([]byte(str), -1) {
		groups := []string{}
		for i := 0; i < len(v); i += 2 {
			groups = append(groups, str[v[i]:v[i+1]])
		}

		result += str[lastIndex:v[0]] + repl(groups)
		lastIndex = v[1]
	}

	return result + str[lastIndex:]
}

func (m *MySqlMacroEngine) evaluateMacro(name string, args []string) (string, error) {
	switch name {
	case "__timeEpoch", "__time":
		if len(args) == 0 {
			return "", fmt.Errorf("missing time column argument for macro %v", name)
		}
		return fmt.Sprintf("UNIX_TIMESTAMP(%s) as time_sec", args[0]), nil
	case "__timeFilter":
		if len(args) == 0 {
			return "", fmt.Errorf("missing time column argument for macro %v", name)
		}
		return fmt.Sprintf("%s >= FROM_UNIXTIME(%d) AND %s <= FROM_UNIXTIME(%d)", args[0], m.TimeRange.GetFromAsSecondsEpoch(), args[0], m.TimeRange.GetToAsSecondsEpoch()), nil
	case "__timeFrom":
		return fmt.Sprintf("FROM_UNIXTIME(%d)", m.TimeRange.GetFromAsSecondsEpoch()), nil
	case "__timeTo":
		return fmt.Sprintf("FROM_UNIXTIME(%d)", m.TimeRange.GetToAsSecondsEpoch()), nil
	case "__timeGroup":
		if len(args) < 2 {
			return "", fmt.Errorf("macro %v needs time column and interval", name)
		}
		interval, err := time.ParseDuration(strings.Trim(args[1], `'"`))
		if err != nil {
			return "", fmt.Errorf("error parsing interval %v", args[1])
		}
		if len(args) == 3 {
			m.Query.Model.Set("fill", true)
			m.Query.Model.Set("fillInterval", interval.Seconds())
			if args[2] == "NULL" {
				m.Query.Model.Set("fillNull", true)
			} else {
				floatVal, err := strconv.ParseFloat(args[2], 64)
				if err != nil {
					return "", fmt.Errorf("error parsing fill value %v", args[2])
				}
				m.Query.Model.Set("fillValue", floatVal)
			}
		}
		return fmt.Sprintf("cast(cast(UNIX_TIMESTAMP(%s)/(%.0f) as signed)*%.0f as signed)", args[0], interval.Seconds(), interval.Seconds()), nil
	case "__unixEpochFilter":
		if len(args) == 0 {
			return "", fmt.Errorf("missing time column argument for macro %v", name)
		}
		return fmt.Sprintf("%s >= %d AND %s <= %d", args[0], m.TimeRange.GetFromAsSecondsEpoch(), args[0], m.TimeRange.GetToAsSecondsEpoch()), nil
	case "__unixEpochFrom":
		return fmt.Sprintf("%d", m.TimeRange.GetFromAsSecondsEpoch()), nil
	case "__unixEpochTo":
		return fmt.Sprintf("%d", m.TimeRange.GetToAsSecondsEpoch()), nil
	case "__wherein":
		if len(args) < 2 {
			return "", fmt.Errorf("macro %v needs column and params", name)
		}
		columnName := args[0]
		if strings.HasPrefix(columnName, "'") {
			columnName = strings.Trim(columnName, "'")
		}
		if (len(args) == 2) && (args[1] == "ALL" || args[1] == "'ALL'") {
			return "1 = 1", nil
		} else if len(args) == 2 && (args[1] == "NULL" || args[1] == "'NULL'") {
			return fmt.Sprintf("%s IS NULL", columnName), nil
		} else {
			var params = make([]string, 0)
			var hasNull = false
			for _, arg := range args[1:] {
				if arg == "NULL" || arg == "'NULL'" {
					hasNull = true
				} else {
					if !strings.HasPrefix(arg, "'") {
						params = append(params, "'"+arg+"'")
					} else {
						params = append(params, arg)
					}
				}
			}
			if hasNull {
				return fmt.Sprintf("%s in (%s) or %s IS NULL", columnName, strings.Join(params, ","), args[0]), nil
			} else {
				return fmt.Sprintf("%s in (%s)", columnName, strings.Join(params, ",")), nil
			}
		}
	default:
		return "", fmt.Errorf("Unknown macro %v", name)
	}
}
