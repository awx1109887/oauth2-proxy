package main

import (
	"encoding/csv"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/oauth2-proxy/oauth2-proxy/v7/pkg/logger"
)

// UserMap holds information from the authenticated emails file
type UserMap struct {
	usersFile string
	m         unsafe.Pointer
}

// NewUserMap parses the authenticated emails file into a new UserMap
//
// TODO (@NickMeves): Audit usage of `unsafe.Pointer` and potentially refactor
func NewUserMap(usersFile string, done <-chan bool, onUpdate func()) *UserMap {
	um := &UserMap{usersFile: usersFile}
	m := make(map[string]bool)
	atomic.StorePointer(&um.m, unsafe.Pointer(&m)) // #nosec G103
	if usersFile != "" {
		logger.Printf("using authenticated emails file %s", usersFile)
		WatchForUpdates(usersFile, done, func() {
			um.LoadAuthenticatedEmailsFile()
			onUpdate()
		})
		um.LoadAuthenticatedEmailsFile()
	}
	return um
}

// IsValid checks if an email is allowed
func (um *UserMap) IsValid(email string) (result bool) {
	m := *(*map[string]bool)(atomic.LoadPointer(&um.m))
	_, result = m[email]
	return
}

// LoadAuthenticatedEmailsFile loads the authenticated emails file from disk
// and parses the contents as CSV
func (um *UserMap) LoadAuthenticatedEmailsFile() {
	r, err := os.Open(um.usersFile)
	if err != nil {
		logger.Fatalf("failed opening authenticated-emails-file=%q, %s", um.usersFile, err)
	}
	defer func(c io.Closer) {
		cerr := c.Close()
		if cerr != nil {
			logger.Fatalf("Error closing authenticated emails file: %s", cerr)
		}
	}(r)
	csvReader := csv.NewReader(r)
	csvReader.Comma = ','
	csvReader.Comment = '#'
	csvReader.TrimLeadingSpace = true
	records, err := csvReader.ReadAll()
	if err != nil {
		logger.Errorf("error reading authenticated-emails-file=%q, %s", um.usersFile, err)
		return
	}
	updated := make(map[string]bool)
	for _, r := range records {
		address := strings.ToLower(strings.TrimSpace(r[0]))
		updated[address] = true
	}
	atomic.StorePointer(&um.m, unsafe.Pointer(&updated)) // #nosec G103
}

func newValidatorImpl(domains []string, usersFile string,
	done <-chan bool, onUpdate func()) func(string) bool {
	validUsers := NewUserMap(usersFile, done, onUpdate)

	var allowAll bool
	for i, domain := range domains {
		if domain == "*" {
			allowAll = true
			continue
		}
		domains[i] = strings.ToLower(domain)
	}

	validator := func(email string) (valid bool) {
		if email == "" {
			return
		}
		email = strings.ToLower(email)
		valid = isEmailValidWithDomains(email, domains)
		if !valid {
			valid = validUsers.IsValid(email)
		}
		if allowAll {
			valid = true
		}
		return valid
	}
	return validator
}

// NewValidator constructs a function to validate email addresses
func NewValidator(domains []string, usersFile string, skipValidateEmail bool) func(string) bool {
	if skipValidateEmail {
		return func(s string) bool {
			logger.Printf("skip validate email %s by config", s)

			return true
		}
	}

	return newValidatorImpl(domains, usersFile, nil, func() {})
}

// isEmailValidWithDomains checks if the authenticated email is validated against the provided domain
func isEmailValidWithDomains(email string, allowedDomains []string) bool {
	for _, domain := range allowedDomains {
		// allow if the domain is perfect suffix match with the email
		if strings.HasSuffix(email, "@"+domain) {
			return true
		}

		// allow if the domain is prefixed with . and
		// the last element (split on @) has the suffix as the domain
		atoms := strings.Split(email, "@")
		if strings.HasPrefix(domain, ".") && strings.HasSuffix(atoms[len(atoms)-1], domain) {
			return true
		}
	}
	return false
}
