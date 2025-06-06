package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/darkweak/souin/configurationtypes"
	"github.com/darkweak/souin/pkg/storage/types"
	"github.com/darkweak/souin/pkg/surrogate/providers"
	"github.com/darkweak/storages/core"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SouinAPI object contains informations related to the endpoints
type SouinAPI struct {
	basePath         string
	enabled          bool
	storers          []types.Storer
	surrogateStorage providers.SurrogateInterface
	allowedMethods   []string
}

type invalidationType string

const (
	uriInvalidationType       invalidationType = "uri"
	uriPrefixInvalidationType invalidationType = "uri-prefix"
	originInvalidationType    invalidationType = "origin"
	groupInvalidationType     invalidationType = "group"
)

type invalidation struct {
	Type      invalidationType `json:"type"`
	Selectors []string         `json:"selectors"`
	Groups    []string         `json:"groups"`
	Purge     bool             `json:"purge"`
}

func initializeSouin(
	configuration configurationtypes.AbstractConfigurationInterface,
	storers []types.Storer,
	surrogateStorage providers.SurrogateInterface,
) *SouinAPI {
	basePath := configuration.GetAPI().Souin.BasePath
	if basePath == "" {
		basePath = "/souin"
	}

	allowedMethods := configuration.GetDefaultCache().GetAllowedHTTPVerbs()
	if len(allowedMethods) == 0 {
		allowedMethods = []string{http.MethodGet, http.MethodHead}
	}

	return &SouinAPI{
		basePath,
		configuration.GetAPI().Souin.Enable,
		storers,
		surrogateStorage,
		allowedMethods,
	}
}

// BulkDelete allow user to delete multiple items with regexp
func (s *SouinAPI) BulkDelete(key string, purge bool) {
	key, _ = strings.CutPrefix(key, core.MappingKeyPrefix)
	for _, current := range s.storers {
		if b := current.Get(core.MappingKeyPrefix + key); len(b) > 0 {
			var mapping core.StorageMapper

			if e := proto.Unmarshal(b, &mapping); e == nil {
				for k := range mapping.GetMapping() {
					current.Delete(k)
				}
			}

			if !purge {
				newFreshTime := time.Now()
				for k, v := range mapping.Mapping {
					v.FreshTime = timestamppb.New(newFreshTime)
					mapping.Mapping[k] = v
				}

				v, e := proto.Marshal(&mapping)
				if e != nil {
					fmt.Println("Impossible to re-encode the mapping", core.MappingKeyPrefix+key)
					current.Delete(core.MappingKeyPrefix + key)
				}
				_ = current.Set(core.MappingKeyPrefix+key, v, storageToInfiniteTTLMap[current.Name()])
			}
		}

		if purge {
			current.Delete(core.MappingKeyPrefix + key)
		}

		current.Delete(key)
	}

	s.Delete(key)
}

// Delete will delete a record into the provider cache system and will update the Souin API if enabled
// The key can be a regexp to delete multiple items
func (s *SouinAPI) Delete(key string) {
	_, err := regexp.Compile(key)
	for _, current := range s.storers {
		if err != nil {
			current.DeleteMany(key)
		} else {
			current.Delete(key)
		}
	}
}

// GetAll will retrieve all stored keys in the provider
func (s *SouinAPI) GetAll() []string {
	keys := []string{}
	for _, current := range s.storers {
		keys = append(keys, current.ListKeys()...)
	}

	return keys
}

// GetBasePath will return the basepath for this resource
func (s *SouinAPI) GetBasePath() string {
	return s.basePath
}

// IsEnabled will return enabled status
func (s *SouinAPI) IsEnabled() bool {
	return s.enabled
}

func (s *SouinAPI) listKeys(search string) []string {
	res := []string{}
	re, err := regexp.Compile(search)
	if err != nil {
		return res
	}
	for _, key := range s.GetAll() {
		if re.MatchString(key) {
			res = append(res, key)
		}
	}

	return res
}

var storageToInfiniteTTLMap = map[string]time.Duration{
	"BADGER":                 types.OneYearDuration,
	"ETCD":                   types.OneYearDuration,
	"GO-REDIS":               0,
	"NUTS":                   0,
	"OLRIC":                  types.OneYearDuration,
	"OTTER":                  types.OneYearDuration,
	"REDIS":                  -1,
	"SIMPLEFS":               0,
	types.DefaultStorageName: types.OneYearDuration,
}

func EvictMapping(current types.Storer) {
	values := current.MapKeys(core.MappingKeyPrefix)
	now := time.Now()
	infiniteStoreDuration := storageToInfiniteTTLMap[current.Name()]

	for k, v := range values {
		mapping := &core.StorageMapper{}

		e := proto.Unmarshal([]byte(v), mapping)
		if e != nil {
			current.Delete(core.MappingKeyPrefix + k)
			continue
		}

		updated := false
		for key, val := range mapping.GetMapping() {
			if now.Sub(val.FreshTime.AsTime()) > 0 && now.Sub(val.StaleTime.AsTime()) > 0 {
				delete(mapping.GetMapping(), key)
				updated = true
			}
		}

		if updated {
			v, e := proto.Marshal(mapping)
			if e != nil {
				fmt.Println("Impossible to re-encode the mapping", core.MappingKeyPrefix+k)
				current.Delete(core.MappingKeyPrefix + k)
			}
			_ = current.Set(core.MappingKeyPrefix+k, v, infiniteStoreDuration)
		}

		if len(mapping.GetMapping()) == 0 {
			current.Delete(core.MappingKeyPrefix + k)
		}
	}
	time.Sleep(time.Minute)
}

func (s *SouinAPI) purgeMapping() {
	for _, current := range s.storers {
		EvictMapping(current)
	}

	fmt.Println("Successfully clear the mappings.")
}

// HandleRequest will handle the request
func (s *SouinAPI) HandleRequest(w http.ResponseWriter, r *http.Request) {
	res := []byte{}
	compile := regexp.MustCompile(s.GetBasePath()+"/.+").FindString(r.RequestURI) != ""
	switch r.Method {
	case http.MethodGet:
		if regexp.MustCompile(s.GetBasePath()+"/surrogate_keys").FindString(r.RequestURI) != "" {
			res, _ = json.Marshal(s.surrogateStorage.List())
		} else if compile {
			search := regexp.MustCompile(s.GetBasePath()+"/(.+)").FindAllStringSubmatch(r.RequestURI, -1)[0][1]
			res, _ = json.Marshal(s.listKeys(search))
			if len(res) == 2 {
				w.WriteHeader(http.StatusNotFound)
			}
		} else {
			res, _ = json.Marshal(s.GetAll())
		}
		w.Header().Set("Content-Type", "application/json")
	case http.MethodPost:
		var invalidator invalidation
		defer func() {
			_ = r.Body.Close()
		}()
		err := json.NewDecoder(r.Body).Decode(&invalidator)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		keysToInvalidate := []string{}
		switch invalidator.Type {
		case groupInvalidationType:
			var surrogateKeys []string
			keysToInvalidate, surrogateKeys = s.surrogateStorage.Purge(http.Header{"Surrogate-Key": invalidator.Groups})
			keysToInvalidate = append(keysToInvalidate, surrogateKeys...)
		case uriPrefixInvalidationType, uriInvalidationType:
			bodyKeys := []string{}
			listedKeys := s.GetAll()
			for _, k := range invalidator.Selectors {
				if !strings.Contains(k, "//") {
					rq, err := http.NewRequest(http.MethodGet, "//"+k, nil)
					if err != nil {
						continue
					}

					bodyKeys = append(bodyKeys, rq.Host+"-"+rq.URL.Path)
				}
			}

			for _, allKey := range listedKeys {
				for _, bk := range bodyKeys {
					if invalidator.Type == uriInvalidationType {
						if strings.Contains(allKey, bk) && strings.Contains(allKey, bk+"-") && strings.HasSuffix(allKey, bk) {
							keysToInvalidate = append(keysToInvalidate, allKey)
							break
						}
					} else {
						if strings.Contains(allKey, bk) &&
							(strings.Contains(allKey, bk+"-") || strings.Contains(allKey, bk+"?") || strings.Contains(allKey, bk+"/") || strings.HasSuffix(allKey, bk)) {
							keysToInvalidate = append(keysToInvalidate, allKey)
							break
						}
					}
				}
			}
		case originInvalidationType:
			bodyKeys := []string{}
			listedKeys := s.GetAll()
			for _, k := range invalidator.Selectors {
				if !strings.Contains(k, "//") {
					rq, err := http.NewRequest(http.MethodGet, "//"+k, nil)
					if err != nil {
						continue
					}

					bodyKeys = append(bodyKeys, rq.Host)
				}
			}

			for _, allKey := range listedKeys {
				for _, bk := range bodyKeys {
					if strings.Contains(allKey, bk) {
						keysToInvalidate = append(keysToInvalidate, allKey)
						break
					}
				}
			}
		}

		for _, k := range keysToInvalidate {
			s.BulkDelete(k, invalidator.Purge)
		}
		w.WriteHeader(http.StatusOK)
	case "PURGE":
		if compile {
			keysRg := regexp.MustCompile(s.GetBasePath() + "/(.+)")
			flushRg := regexp.MustCompile(s.GetBasePath() + "/flush$")
			mappingRg := regexp.MustCompile(s.GetBasePath() + "/mapping$")

			if flushRg.FindString(r.RequestURI) != "" {
				for _, current := range s.storers {
					current.DeleteMany(".+")
				}
				e := s.surrogateStorage.Destruct()
				if e != nil {
					fmt.Printf("Error while purging the surrogate keys: %+v.", e)
				}
				fmt.Println("Successfully clear the cache and the surrogate keys storage.")
			} else if mappingRg.FindString(r.RequestURI) != "" {
				s.purgeMapping()
			} else {
				submatch := keysRg.FindAllStringSubmatch(r.RequestURI, -1)[0][1]
				for _, current := range s.storers {
					current.DeleteMany(submatch)
				}
			}
		} else {
			ck, surrogateKeys := s.surrogateStorage.Purge(r.Header)
			for _, k := range ck {
				s.BulkDelete(k, true)
			}
			for _, k := range surrogateKeys {
				s.BulkDelete("SURROGATE_"+k, true)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
	}
	_, _ = w.Write(res)
}
