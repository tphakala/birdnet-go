package birdnet

import (
    "bufio"
    "fmt"
    "os"
    "strconv"
    "strings"
)

// Label represents a species label with a count.
type Label struct {
    Species string
    Count   int
}

// ParseLabelLine parses a single label line of the form "species,count".
func ParseLabelLine(line string) (Label, error) {
    trimmed := strings.TrimSpace(line)
    if trimmed == "" {
        return Label{}, fmt.Errorf("empty line")
    }
    parts := strings.Split(trimmed, ",")
    if len(parts) != 2 {
        return Label{}, fmt.Errorf("invalid format: %q", line)
    }
    species := strings.TrimSpace(parts[0])
    countStr := strings.TrimSpace(parts[1])
    if species == "" {
        return Label{}, fmt.Errorf("empty species in line: %q", line)
    }
    count, err := strconv.Atoi(countStr)
    if err != nil {
        return Label{}, fmt.Errorf("invalid count %q: %v", countStr, err)
    }
    if count < 0 {
        return Label{}, fmt.Errorf("negative count %d", count)
    }
    return Label{Species: species, Count: count}, nil
}

// LoadLabels loads labels from a file, skipping empty lines and comments.
func LoadLabels(path string) ([]Label, error) {
    file, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer file.Close()

    var labels []Label
    scanner := bufio.NewScanner(file)
    for scanner.Scan() {
        line := scanner.Text()
        trimmed := strings.TrimSpace(line)
        if trimmed == "" || strings.HasPrefix(trimmed, "#") {
            continue
        }
        lbl, err := ParseLabelLine(trimmed)
        if err != nil {
            return nil, err
        }
        labels = append(labels, lbl)
    }
    if err := scanner.Err(); err != nil {
        return nil, err
    }
    return labels, nil
}

// WriteLabels writes labels to a CSV file at the given path.
func WriteLabels(path string, labels []Label) error {
    file, err := os.Create(path)
    if err != nil {
        return err
    }
    defer file.Close()

    for _, lbl := range labels {
        line := fmt.Sprintf("%s,%d\n", lbl.Species, lbl.Count)
        if _, err := file.WriteString(line); err != nil {
            return err
        }
    }
    return nil
}