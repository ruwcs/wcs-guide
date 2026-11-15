package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalid обозначает нарушение инварианта модели.
var ErrInvalid = errors.New("некорректная доменная модель")

// invalid возвращает ошибку проверки данных.
func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalid, field) }

// blank проверяет отсутствие непробельных символов.
func blank(s string) bool { return strings.TrimSpace(s) == "" }
