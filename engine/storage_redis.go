/*
Real-time Charging System for Telecom & ISP environments
Copyright (C) 2012-2015 ITsysCOM GmbH

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>
*/

package engine

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/cgrates/cgrates/cache2go"
	"github.com/cgrates/cgrates/config"
	"github.com/cgrates/cgrates/utils"
	"github.com/mediocregopher/radix.v2/pool"
	"github.com/mediocregopher/radix.v2/redis"
)

var (
	ErrRedisNotFound = errors.New("RedisNotFound")
)

type RedisStorage struct {
	db              *pool.Pool
	ms              Marshaler
	cacheCfg        *config.CacheConfig
	loadHistorySize int
}

func NewRedisStorage(address string, db int, pass, mrshlerStr string, maxConns int, cacheCfg *config.CacheConfig, loadHistorySize int) (*RedisStorage, error) {
	df := func(network, addr string) (*redis.Client, error) {
		client, err := redis.Dial(network, addr)
		if err != nil {
			return nil, err
		}
		if len(pass) != 0 {
			if err = client.Cmd("AUTH", pass).Err; err != nil {
				client.Close()
				return nil, err
			}
		}
		if db != 0 {
			if err = client.Cmd("SELECT", db).Err; err != nil {
				client.Close()
				return nil, err
			}
		}
		return client, nil
	}
	p, err := pool.NewCustom("tcp", address, maxConns, df)
	if err != nil {
		return nil, err
	}
	var mrshler Marshaler
	if mrshlerStr == utils.MSGPACK {
		mrshler = NewCodecMsgpackMarshaler()
	} else if mrshlerStr == utils.JSON {
		mrshler = new(JSONMarshaler)
	} else {
		return nil, fmt.Errorf("Unsupported marshaler: %v", mrshlerStr)
	}
	return &RedisStorage{db: p, ms: mrshler, cacheCfg: cacheCfg, loadHistorySize: loadHistorySize}, nil
}

func (rs *RedisStorage) Close() {
	rs.db.Empty()
}

func (rs *RedisStorage) Flush(ignore string) error {
	return rs.db.Cmd("FLUSHDB").Err
}

func (rs *RedisStorage) PreloadRatingCache() error {
	if rs.cacheCfg == nil {
		return nil
	}
	if rs.cacheCfg.Destinations != nil && rs.cacheCfg.Destinations.Precache {
		if err := rs.PreloadCacheForPrefix(utils.DESTINATION_PREFIX); err != nil {
			return err
		}
	}

	if rs.cacheCfg.ReverseDestinations != nil && rs.cacheCfg.ReverseDestinations.Precache {
		if err := rs.PreloadCacheForPrefix(utils.REVERSE_DESTINATION_PREFIX); err != nil {
			return err
		}
	}

	if rs.cacheCfg.RatingPlans != nil && rs.cacheCfg.RatingPlans.Precache {
		if err := rs.PreloadCacheForPrefix(utils.RATING_PLAN_PREFIX); err != nil {
			return err
		}
	}

	if rs.cacheCfg.RatingProfiles != nil && rs.cacheCfg.RatingProfiles.Precache {
		if err := rs.PreloadCacheForPrefix(utils.RATING_PROFILE_PREFIX); err != nil {
			return err
		}
	}
	if rs.cacheCfg.Lcr != nil && rs.cacheCfg.Lcr.Precache {
		if err := rs.PreloadCacheForPrefix(utils.LCR_PREFIX); err != nil {
			return err
		}
	}
	if rs.cacheCfg.CdrStats != nil && rs.cacheCfg.CdrStats.Precache {
		if err := rs.PreloadCacheForPrefix(utils.CDR_STATS_PREFIX); err != nil {
			return err
		}
	}
	if rs.cacheCfg.Actions != nil && rs.cacheCfg.Actions.Precache {
		if err := rs.PreloadCacheForPrefix(utils.ACTION_PREFIX); err != nil {
			return err
		}
	}
	if rs.cacheCfg.ActionPlans != nil && rs.cacheCfg.ActionPlans.Precache {
		if err := rs.PreloadCacheForPrefix(utils.ACTION_PLAN_PREFIX); err != nil {
			return err
		}
	}
	if rs.cacheCfg.ActionTriggers != nil && rs.cacheCfg.ActionTriggers.Precache {
		if err := rs.PreloadCacheForPrefix(utils.ACTION_TRIGGER_PREFIX); err != nil {
			return err
		}
	}
	if rs.cacheCfg.SharedGroups != nil && rs.cacheCfg.SharedGroups.Precache {
		if err := rs.PreloadCacheForPrefix(utils.SHARED_GROUP_PREFIX); err != nil {
			return err
		}
	}
	// add more prefixes if needed
	return nil
}

func (rs *RedisStorage) PreloadAccountingCache() error {
	if rs.cacheCfg == nil {
		return nil
	}
	if rs.cacheCfg.Aliases != nil && rs.cacheCfg.Aliases.Precache {
		if err := rs.PreloadCacheForPrefix(utils.ALIASES_PREFIX); err != nil {
			return err
		}
	}

	if rs.cacheCfg.ReverseAliases != nil && rs.cacheCfg.ReverseAliases.Precache {
		if err := rs.PreloadCacheForPrefix(utils.REVERSE_ALIASES_PREFIX); err != nil {
			return err
		}
	}
	return nil
}

func (rs *RedisStorage) PreloadCacheForPrefix(prefix string) error {
	cache2go.BeginTransaction()
	cache2go.RemPrefixKey(prefix)
	keyList, err := rs.GetKeysForPrefix(prefix)
	if err != nil {
		cache2go.RollbackTransaction()
		return err
	}
	switch prefix {
	case utils.RATING_PLAN_PREFIX:
		for _, key := range keyList {
			_, err := rs.GetRatingPlan(key[len(utils.RATING_PLAN_PREFIX):], true)
			if err != nil {
				cache2go.RollbackTransaction()
				return err
			}
		}
	case utils.ResourceLimitsPrefix:
		for _, key := range keyList {
			_, err = rs.GetResourceLimit(key[len(utils.ResourceLimitsPrefix):], true)
			if err != nil {
				cache2go.RollbackTransaction()
				return err
			}
		}
	default:
		return utils.ErrInvalidKey
	}
	cache2go.CommitTransaction()
	return nil
}

func (rs *RedisStorage) RebuildReverseForPrefix(prefix string) error {
	conn, err := rs.db.Get()
	if err != nil {
		return err
	}
	defer rs.db.Put(conn)
	keys, err := rs.GetKeysForPrefix(prefix)
	if err != nil {
		return err
	}
	for _, key := range keys {
		err = conn.Cmd("DEL", key).Err
		if err != nil {
			return err
		}
	}
	switch prefix {
	case utils.REVERSE_DESTINATION_PREFIX:
		keys, err = rs.GetKeysForPrefix(utils.DESTINATION_PREFIX)
		if err != nil {
			return err
		}
		for _, key := range keys {
			dest, err := rs.GetDestination(key[len(utils.DESTINATION_PREFIX):], false)
			if err != nil {
				return err
			}
			if err := rs.SetReverseDestination(dest); err != nil {
				return err
			}
		}
	case utils.REVERSE_ALIASES_PREFIX:
		keys, err = rs.GetKeysForPrefix(utils.ALIASES_PREFIX)
		if err != nil {
			return err
		}
		for _, key := range keys {
			al, err := rs.GetAlias(key[len(utils.ALIASES_PREFIX):], false)
			if err != nil {
				return err
			}
			if err := rs.SetReverseAlias(al); err != nil {
				return err
			}
		}
	default:
		return utils.ErrInvalidKey
	}
	return nil
}

func (rs *RedisStorage) GetKeysForPrefix(prefix string) ([]string, error) {
	r := rs.db.Cmd("KEYS", prefix+"*")
	if r.Err != nil {
		return nil, r.Err
	}
	return r.List()
}

// Used to check if specific subject is stored using prefix key attached to entity
func (rs *RedisStorage) HasData(category, subject string) (bool, error) {
	switch category {
	case utils.DESTINATION_PREFIX, utils.RATING_PLAN_PREFIX, utils.RATING_PROFILE_PREFIX, utils.ACTION_PREFIX, utils.ACTION_PLAN_PREFIX, utils.ACCOUNT_PREFIX, utils.DERIVEDCHARGERS_PREFIX:
		i, err := rs.db.Cmd("EXISTS", category+subject).Int()
		return i == 1, err
	}
	return false, errors.New("unsupported HasData category")
}

func (rs *RedisStorage) GetRatingPlan(key string, skipCache bool) (rp *RatingPlan, err error) {
	key = utils.RATING_PLAN_PREFIX + key
	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				return x.(*RatingPlan), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); err == nil {
		b := bytes.NewBuffer(values)
		r, err := zlib.NewReader(b)
		if err != nil {
			return nil, err
		}
		out, err := ioutil.ReadAll(r)
		if err != nil {
			return nil, err
		}
		r.Close()
		rp = new(RatingPlan)
		err = rs.ms.Unmarshal(out, rp)
	}
	cache2go.Set(key, rp)
	return
}

func (rs *RedisStorage) SetRatingPlan(rp *RatingPlan) (err error) {
	result, err := rs.ms.Marshal(rp)
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(result)
	w.Close()
	err = rs.db.Cmd("SET", utils.RATING_PLAN_PREFIX+rp.Id, b.Bytes()).Err
	if err == nil && historyScribe != nil {
		response := 0
		go historyScribe.Call("HistoryV1.Record", rp.GetHistoryRecord(), &response)
	}
	cache2go.Set(utils.RATING_PLAN_PREFIX+rp.Id, rp)
	return
}

func (rs *RedisStorage) GetRatingProfile(key string, skipCache bool) (rpf *RatingProfile, err error) {
	key = utils.RATING_PROFILE_PREFIX + key

	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				return x.(*RatingProfile), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); err == nil {
		rpf = new(RatingProfile)
		err = rs.ms.Unmarshal(values, rpf)
	}
	cache2go.Set(key, rpf)
	return
}

func (rs *RedisStorage) SetRatingProfile(rpf *RatingProfile) (err error) {
	result, err := rs.ms.Marshal(rpf)
	err = rs.db.Cmd("SET", utils.RATING_PROFILE_PREFIX+rpf.Id, result).Err
	if err == nil && historyScribe != nil {
		response := 0
		go historyScribe.Call("HistoryV1.Record", rpf.GetHistoryRecord(false), &response)
	}
	cache2go.RemKey(utils.RATING_PROFILE_PREFIX + rpf.Id)
	return
}

func (rs *RedisStorage) RemoveRatingProfile(key string) error {
	conn, err := rs.db.Get()
	if err != nil {
		return err
	}
	defer rs.db.Put(conn)
	keys, err := conn.Cmd("KEYS", utils.RATING_PROFILE_PREFIX+key+"*").List()
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err = conn.Cmd("DEL", key).Err; err != nil {
			return err
		}
		cache2go.RemKey(key)
		rpf := &RatingProfile{Id: key}
		if historyScribe != nil {
			response := 0
			go historyScribe.Call("HistoryV1.Record", rpf.GetHistoryRecord(true), &response)
		}
	}
	return nil
}

func (rs *RedisStorage) GetLCR(key string, skipCache bool) (lcr *LCR, err error) {
	key = utils.LCR_PREFIX + key
	if !skipCache {
		if x, ok := cache2go.Get(key); ok {

			if x != nil {
				return x.(*LCR), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); err == nil {
		err = rs.ms.Unmarshal(values, &lcr)
	} else {
		cache2go.Set(key, nil)
		return nil, utils.ErrNotFound
	}
	cache2go.Set(key, lcr)
	return
}

func (rs *RedisStorage) SetLCR(lcr *LCR) (err error) {
	result, err := rs.ms.Marshal(lcr)
	key := utils.LCR_PREFIX + lcr.GetId()
	err = rs.db.Cmd("SET", key, result).Err
	cache2go.RemKey(key)
	return
}

func (rs *RedisStorage) GetDestination(key string, skipCache bool) (dest *Destination, err error) {
	key = utils.DESTINATION_PREFIX + key
	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				return x.(*Destination), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); len(values) > 0 && err == nil {
		b := bytes.NewBuffer(values)
		r, err := zlib.NewReader(b)
		if err != nil {
			return nil, err
		}
		out, err := ioutil.ReadAll(r)
		if err != nil {
			return nil, err
		}
		r.Close()
		dest = new(Destination)
		err = rs.ms.Unmarshal(out, dest)
		if err != nil {
			cache2go.Set(key, dest)
		}
	} else {
		cache2go.Set(key, nil)
		return nil, err
	}
	return
}

func (rs *RedisStorage) SetDestination(dest *Destination) (err error) {
	result, err := rs.ms.Marshal(dest)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(result)
	w.Close()
	key := utils.DESTINATION_PREFIX + dest.Id
	err = rs.db.Cmd("SET", key, b.Bytes()).Err
	if err == nil && historyScribe != nil {
		response := 0
		go historyScribe.Call("HistoryV1.Record", dest.GetHistoryRecord(false), &response)
	}
	cache2go.RemKey(key)
	return
}

func (rs *RedisStorage) GetReverseDestination(prefix string, skipCache bool) (ids []string, err error) {
	prefix = utils.REVERSE_DESTINATION_PREFIX + prefix
	if !skipCache {
		if x, ok := cache2go.Get(prefix); ok {
			if x != nil {
				return x.([]string), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	if ids, err = rs.db.Cmd("SMEMBERS", prefix).List(); len(ids) > 0 && err == nil {
		cache2go.Set(prefix, ids)
		return ids, nil
	}
	return nil, utils.ErrNotFound
}

func (rs *RedisStorage) SetReverseDestination(dest *Destination) (err error) {
	for _, p := range dest.Prefixes {
		key := utils.REVERSE_DESTINATION_PREFIX + p
		err = rs.db.Cmd("SADD", key, dest.Id).Err
		if err != nil {
			break
		}
		cache2go.RemKey(key)
	}
	return
}

func (rs *RedisStorage) RemoveDestination(destID string) (err error) {
	key := utils.DESTINATION_PREFIX + destID
	// get destination for prefix list
	d, err := rs.GetDestination(destID, false)
	if err != nil {
		return
	}
	err = rs.db.Cmd("DEL", key).Err
	if err != nil {
		return err
	}
	cache2go.RemKey(key)
	for _, prefix := range d.Prefixes {
		err = rs.db.Cmd("SREM", utils.REVERSE_DESTINATION_PREFIX+prefix, destID).Err
		if err != nil {
			return err
		}
		rs.GetReverseDestination(prefix, true) // it will recache the destination
	}
	return
}

func (rs *RedisStorage) UpdateReverseDestination(oldDest, newDest *Destination) error {
	//log.Printf("Old: %+v, New: %+v", oldDest, newDest)
	var obsoletePrefixes []string
	var addedPrefixes []string
	var found bool
	for _, oldPrefix := range oldDest.Prefixes {
		found = false
		for _, newPrefix := range newDest.Prefixes {
			if oldPrefix == newPrefix {
				found = true
				break
			}
		}
		if !found {
			obsoletePrefixes = append(obsoletePrefixes, oldPrefix)
		}
	}

	for _, newPrefix := range newDest.Prefixes {
		found = false
		for _, oldPrefix := range oldDest.Prefixes {
			if newPrefix == oldPrefix {
				found = true
				break
			}
		}
		if !found {
			addedPrefixes = append(addedPrefixes, newPrefix)
		}
	}
	//log.Print("Obsolete prefixes: ", obsoletePrefixes)
	//log.Print("Added prefixes: ", addedPrefixes)
	// remove id for all obsolete prefixes
	var err error
	for _, obsoletePrefix := range obsoletePrefixes {
		err = rs.db.Cmd("SREM", utils.REVERSE_DESTINATION_PREFIX+obsoletePrefix, oldDest.Id).Err
		if err != nil {
			return err
		}
		cache2go.RemKey(utils.REVERSE_DESTINATION_PREFIX + obsoletePrefix)
	}

	// add the id to all new prefixes
	for _, addedPrefix := range addedPrefixes {
		err = rs.db.Cmd("SADD", utils.REVERSE_DESTINATION_PREFIX+addedPrefix, newDest.Id).Err
		if err != nil {
			return err
		}
		cache2go.RemKey(utils.REVERSE_DESTINATION_PREFIX + addedPrefix)
	}
	return nil
}

func (rs *RedisStorage) GetActions(key string, skipCache bool) (as Actions, err error) {
	key = utils.ACTION_PREFIX + key
	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				return x.(Actions), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); err == nil {
		err = rs.ms.Unmarshal(values, &as)
	}
	cache2go.Set(key, as)
	return
}

func (rs *RedisStorage) SetActions(key string, as Actions) (err error) {
	result, err := rs.ms.Marshal(&as)
	err = rs.db.Cmd("SET", utils.ACTION_PREFIX+key, result).Err
	cache2go.RemKey(utils.ACTION_PREFIX + key)
	return
}

func (rs *RedisStorage) RemoveActions(key string) (err error) {
	err = rs.db.Cmd("DEL", utils.ACTION_PREFIX+key).Err
	cache2go.RemKey(utils.ACTION_PREFIX + key)
	return
}

func (rs *RedisStorage) GetSharedGroup(key string, skipCache bool) (sg *SharedGroup, err error) {
	key = utils.SHARED_GROUP_PREFIX + key
	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				return x.(*SharedGroup), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); err == nil {
		err = rs.ms.Unmarshal(values, &sg)
	}
	cache2go.Set(key, sg)
	return
}

func (rs *RedisStorage) SetSharedGroup(sg *SharedGroup) (err error) {
	result, err := rs.ms.Marshal(sg)
	err = rs.db.Cmd("SET", utils.SHARED_GROUP_PREFIX+sg.Id, result).Err
	cache2go.RemKey(utils.SHARED_GROUP_PREFIX + sg.Id)
	return
}

func (rs *RedisStorage) GetAccount(key string) (*Account, error) {
	rpl := rs.db.Cmd("GET", utils.ACCOUNT_PREFIX+key)
	if rpl.Err != nil {
		return nil, rpl.Err
	} else if rpl.IsType(redis.Nil) {
		return nil, ErrRedisNotFound
	}
	values, err := rpl.Bytes()
	if err != nil {
		return nil, err
	}
	ub := &Account{ID: key}
	if err = rs.ms.Unmarshal(values, ub); err != nil {
		return nil, err
	}
	return ub, nil
}

func (rs *RedisStorage) SetAccount(ub *Account) (err error) {
	// never override existing account with an empty one
	// UPDATE: if all balances expired and were cleaned it makes
	// sense to write empty balance map
	if len(ub.BalanceMap) == 0 {
		if ac, err := rs.GetAccount(ub.ID); err == nil && !ac.allBalancesExpired() {
			ac.ActionTriggers = ub.ActionTriggers
			ac.UnitCounters = ub.UnitCounters
			ac.AllowNegative = ub.AllowNegative
			ac.Disabled = ub.Disabled
			ub = ac
		}
	}
	result, err := rs.ms.Marshal(ub)
	err = rs.db.Cmd("SET", utils.ACCOUNT_PREFIX+ub.ID, result).Err
	return
}

func (rs *RedisStorage) RemoveAccount(key string) (err error) {
	return rs.db.Cmd("DEL", utils.ACCOUNT_PREFIX+key).Err

}

func (rs *RedisStorage) GetCdrStatsQueue(key string) (sq *StatsQueue, err error) {
	var values []byte
	if values, err = rs.db.Cmd("GET", utils.CDR_STATS_QUEUE_PREFIX+key).Bytes(); err == nil {
		sq = &StatsQueue{}
		err = rs.ms.Unmarshal(values, &sq)
	}
	return
}

func (rs *RedisStorage) SetCdrStatsQueue(sq *StatsQueue) (err error) {
	result, err := rs.ms.Marshal(sq)
	err = rs.db.Cmd("SET", utils.CDR_STATS_QUEUE_PREFIX+sq.GetId(), result).Err
	return
}

func (rs *RedisStorage) GetSubscribers() (result map[string]*SubscriberData, err error) {
	conn, err := rs.db.Get()
	if err != nil {
		return nil, err
	}
	defer rs.db.Put(conn)
	keys, err := conn.Cmd("KEYS", utils.PUBSUB_SUBSCRIBERS_PREFIX+"*").List()
	if err != nil {
		return nil, err
	}
	result = make(map[string]*SubscriberData)
	for _, key := range keys {
		if values, err := conn.Cmd("GET", key).Bytes(); err == nil {
			sub := &SubscriberData{}
			err = rs.ms.Unmarshal(values, sub)
			result[key[len(utils.PUBSUB_SUBSCRIBERS_PREFIX):]] = sub
		} else {
			return nil, utils.ErrNotFound
		}
	}
	return
}

func (rs *RedisStorage) SetSubscriber(key string, sub *SubscriberData) (err error) {
	result, err := rs.ms.Marshal(sub)
	if err != nil {
		return err
	}
	return rs.db.Cmd("SET", utils.PUBSUB_SUBSCRIBERS_PREFIX+key, result).Err
}

func (rs *RedisStorage) RemoveSubscriber(key string) (err error) {
	err = rs.db.Cmd("DEL", utils.PUBSUB_SUBSCRIBERS_PREFIX+key).Err
	return
}

func (rs *RedisStorage) SetUser(up *UserProfile) (err error) {
	result, err := rs.ms.Marshal(up)
	if err != nil {
		return err
	}
	return rs.db.Cmd("SET", utils.USERS_PREFIX+up.GetId(), result).Err
}

func (rs *RedisStorage) GetUser(key string) (up *UserProfile, err error) {
	var values []byte
	if values, err = rs.db.Cmd("GET", utils.USERS_PREFIX+key).Bytes(); err == nil {
		up = &UserProfile{}
		err = rs.ms.Unmarshal(values, &up)
	}
	return
}

func (rs *RedisStorage) GetUsers() (result []*UserProfile, err error) {
	conn, err := rs.db.Get()
	if err != nil {
		return nil, err
	}
	defer rs.db.Put(conn)
	keys, err := conn.Cmd("KEYS", utils.USERS_PREFIX+"*").List()
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if values, err := conn.Cmd("GET", key).Bytes(); err == nil {
			up := &UserProfile{}
			err = rs.ms.Unmarshal(values, up)
			result = append(result, up)
		} else {
			return nil, utils.ErrNotFound
		}
	}
	return
}

func (rs *RedisStorage) RemoveUser(key string) (err error) {
	return rs.db.Cmd("DEL", utils.USERS_PREFIX+key).Err
}

func (rs *RedisStorage) GetAlias(key string, skipCache bool) (al *Alias, err error) {
	origKey := key
	key = utils.ALIASES_PREFIX + key

	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				al = &Alias{Values: x.(AliasValues)}
				al.SetId(origKey)
				return al, nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); err == nil {
		al = &Alias{Values: make(AliasValues, 0)}
		al.SetId(origKey)
		err = rs.ms.Unmarshal(values, &al.Values)
	} else {
		cache2go.Set(key, nil)
		return nil, utils.ErrNotFound
	}
	cache2go.Set(key, al.Values)
	return
}

func (rs *RedisStorage) SetAlias(al *Alias) (err error) {
	result, err := rs.ms.Marshal(al.Values)
	if err != nil {
		return err
	}
	key := utils.ALIASES_PREFIX + al.GetId()
	err = rs.db.Cmd("SET", key, result).Err
	cache2go.RemKey(key)
	return
}

func (rs *RedisStorage) GetReverseAlias(reverseID string, skipCache bool) (ids []string, err error) {
	key := utils.REVERSE_ALIASES_PREFIX + reverseID
	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				return x.([]string), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	if ids, err = rs.db.Cmd("SMEMBERS", key).List(); len(ids) == 0 || err != nil {
		cache2go.Set(key, nil)
		return nil, utils.ErrNotFound
	}
	cache2go.Set(key, ids)
	return
}

func (rs *RedisStorage) SetReverseAlias(al *Alias) (err error) {
	for _, value := range al.Values {
		for target, pairs := range value.Pairs {
			for _, alias := range pairs {
				rKey := strings.Join([]string{utils.REVERSE_ALIASES_PREFIX, alias, target, al.Context}, "")
				id := utils.ConcatenatedKey(al.GetId(), value.DestinationId)
				err = rs.db.Cmd("SADD", rKey, id).Err
				if err != nil {
					break
				}
				cache2go.RemKey(rKey)
			}
		}
	}

	return
}

func (rs *RedisStorage) RemoveAlias(id string) (err error) {
	key := utils.ALIASES_PREFIX + id
	// get alias for values list
	al, err := rs.GetAlias(id, false)
	if err != nil {
		return
	}
	err = rs.db.Cmd("DEL", key).Err
	if err != nil {
		return err
	}
	cache2go.RemKey(key)

	for _, value := range al.Values {
		tmpKey := utils.ConcatenatedKey(al.GetId(), value.DestinationId)
		for target, pairs := range value.Pairs {
			for _, alias := range pairs {
				rKey := utils.REVERSE_ALIASES_PREFIX + alias + target + al.Context
				err = rs.db.Cmd("SREM", rKey, tmpKey).Err
				if err != nil {
					return err
				}
				cache2go.RemKey(rKey)
				/*_, err = rs.GetReverseAlias(rKey, true) // recache
				if err != nil {
					return err
				}*/
			}
		}
	}
	return
}

func (rs *RedisStorage) UpdateReverseAlias(oldAl, newAl *Alias) error {
	// FIXME: thi can be optimized
	cache2go.RemPrefixKey(utils.REVERSE_ALIASES_PREFIX)
	rs.SetReverseAlias(newAl)
	return nil
}

// Limit will only retrieve the last n items out of history, newest first
func (rs *RedisStorage) GetLoadHistory(limit int, skipCache bool) ([]*utils.LoadInstance, error) {
	if limit == 0 {
		return nil, nil
	}

	if !skipCache {
		if x, ok := cache2go.Get(utils.LOADINST_KEY); ok {
			if x != nil {
				items := x.([]*utils.LoadInstance)
				if len(items) < limit || limit == -1 {
					return items, nil
				}
				return items[:limit], nil
			}
			return nil, utils.ErrNotFound
		}
	}
	if limit != -1 {
		limit -= -1 // Decrease limit to match redis approach on lrange
	}
	marshaleds, err := rs.db.Cmd("LRANGE", utils.LOADINST_KEY, 0, limit).ListBytes()
	if err != nil {
		cache2go.Set(utils.LOADINST_KEY, nil)
		return nil, err
	}
	loadInsts := make([]*utils.LoadInstance, len(marshaleds))
	for idx, marshaled := range marshaleds {
		var lInst utils.LoadInstance
		err = rs.ms.Unmarshal(marshaled, &lInst)
		if err != nil {
			return nil, err
		}
		loadInsts[idx] = &lInst
	}
	cache2go.RemKey(utils.LOADINST_KEY)
	cache2go.Set(utils.LOADINST_KEY, loadInsts)
	if len(loadInsts) < limit || limit == -1 {
		return loadInsts, nil
	}
	return loadInsts[:limit], nil
}

// Adds a single load instance to load history
func (rs *RedisStorage) AddLoadHistory(ldInst *utils.LoadInstance, loadHistSize int) error {
	conn, err := rs.db.Get()
	if err != nil {
		return err
	}
	defer rs.db.Put(conn)
	if loadHistSize == 0 { // Load history disabled
		return nil
	}
	marshaled, err := rs.ms.Marshal(&ldInst)
	if err != nil {
		return err
	}
	_, err = Guardian.Guard(func() (interface{}, error) { // Make sure we do it locked since other instance can modify history while we read it
		histLen, err := conn.Cmd("LLEN", utils.LOADINST_KEY).Int()
		if err != nil {
			return nil, err
		}
		if histLen >= loadHistSize { // Have hit maximum history allowed, remove oldest element in order to add new one
			if err := conn.Cmd("RPOP", utils.LOADINST_KEY).Err; err != nil {
				return nil, err
			}
		}
		err = conn.Cmd("LPUSH", utils.LOADINST_KEY, marshaled).Err
		return nil, err
	}, 0, utils.LOADINST_KEY)

	cache2go.RemKey(utils.LOADINST_KEY)
	return err
}

func (rs *RedisStorage) GetActionTriggers(key string, skipCache bool) (atrs ActionTriggers, err error) {
	key = utils.ACTION_TRIGGER_PREFIX + key
	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				return x.(ActionTriggers), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); err == nil {
		err = rs.ms.Unmarshal(values, &atrs)
	}
	cache2go.Set(key, atrs)
	return
}

func (rs *RedisStorage) SetActionTriggers(key string, atrs ActionTriggers) (err error) {
	conn, err := rs.db.Get()
	if err != nil {
		return err
	}
	defer rs.db.Put(conn)
	if len(atrs) == 0 {
		// delete the key
		return conn.Cmd("DEL", utils.ACTION_TRIGGER_PREFIX+key).Err
	}
	result, err := rs.ms.Marshal(atrs)
	if err != nil {
		return err
	}
	err = conn.Cmd("SET", utils.ACTION_TRIGGER_PREFIX+key, result).Err
	cache2go.RemKey(utils.ACTION_TRIGGER_PREFIX + key)
	return
}

func (rs *RedisStorage) RemoveActionTriggers(key string) (err error) {
	key = utils.ACTION_TRIGGER_PREFIX + key
	err = rs.db.Cmd("DEL", key).Err
	cache2go.RemKey(key)

	return
}

func (rs *RedisStorage) GetActionPlan(key string, skipCache bool) (ats *ActionPlan, err error) {
	key = utils.ACTION_PLAN_PREFIX + key
	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				return x.(*ActionPlan), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); err == nil {
		b := bytes.NewBuffer(values)
		r, err := zlib.NewReader(b)
		if err != nil {
			return nil, err
		}
		out, err := ioutil.ReadAll(r)
		if err != nil {
			return nil, err
		}
		r.Close()
		ats = &ActionPlan{}
		err = rs.ms.Unmarshal(out, &ats)
	}
	cache2go.Set(key, ats)
	return
}

func (rs *RedisStorage) SetActionPlan(key string, ats *ActionPlan, overwrite bool) (err error) {
	if len(ats.ActionTimings) == 0 {
		// delete the key
		err = rs.db.Cmd("DEL", utils.ACTION_PLAN_PREFIX+key).Err
		cache2go.RemKey(utils.ACTION_PLAN_PREFIX + key)
		return err
	}
	if !overwrite {
		// get existing action plan to merge the account ids
		if existingAts, _ := rs.GetActionPlan(key, true); existingAts != nil {
			if ats.AccountIDs == nil && len(existingAts.AccountIDs) > 0 {
				ats.AccountIDs = make(utils.StringMap)
			}
			for accID := range existingAts.AccountIDs {
				ats.AccountIDs[accID] = true
			}
		}
		// do not keep this in cache (will be obsolete)
		cache2go.RemKey(utils.ACTION_PLAN_PREFIX + key)
	}
	result, err := rs.ms.Marshal(ats)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(result)
	w.Close()
	err = rs.db.Cmd("SET", utils.ACTION_PLAN_PREFIX+key, b.Bytes()).Err
	cache2go.RemKey(utils.ACTION_PLAN_PREFIX + key)
	return
}

func (rs *RedisStorage) GetAllActionPlans() (ats map[string]*ActionPlan, err error) {

	keys, err := rs.GetKeysForPrefix(utils.ACTION_PLAN_PREFIX)
	if err != nil {
		return nil, err
	}

	ats = make(map[string]*ActionPlan, len(keys))
	for _, key := range keys {
		ap, err := rs.GetActionPlan(key[len(utils.ACTION_PLAN_PREFIX):], false)
		if err != nil {
			return nil, err
		}
		ats[key[len(utils.ACTION_PLAN_PREFIX):]] = ap
	}

	return
}

func (rs *RedisStorage) PushTask(t *Task) error {
	result, err := rs.ms.Marshal(t)
	if err != nil {
		return err
	}
	return rs.db.Cmd("RPUSH", utils.TASKS_KEY, result).Err
}

func (rs *RedisStorage) PopTask() (t *Task, err error) {
	var values []byte
	if values, err = rs.db.Cmd("LPOP", utils.TASKS_KEY).Bytes(); err == nil {
		t = &Task{}
		err = rs.ms.Unmarshal(values, t)
	}
	return
}

func (rs *RedisStorage) GetDerivedChargers(key string, skipCache bool) (dcs *utils.DerivedChargers, err error) {
	key = utils.DERIVEDCHARGERS_PREFIX + key
	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				return x.(*utils.DerivedChargers), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); err == nil {
		err = rs.ms.Unmarshal(values, &dcs)
	} else {
		cache2go.Set(key, nil)
		return nil, utils.ErrNotFound
	}
	cache2go.Set(key, dcs)
	return
}

func (rs *RedisStorage) SetDerivedChargers(key string, dcs *utils.DerivedChargers) (err error) {
	key = utils.DERIVEDCHARGERS_PREFIX + key
	if dcs == nil || len(dcs.Chargers) == 0 {
		err = rs.db.Cmd("DEL", key).Err
		cache2go.RemKey(key)
		return err
	}
	marshaled, err := rs.ms.Marshal(dcs)
	if err != nil {
		return err
	}
	err = rs.db.Cmd("SET", key, marshaled).Err
	cache2go.RemKey(key)
	return
}

func (rs *RedisStorage) SetCdrStats(cs *CdrStats) error {
	marshaled, err := rs.ms.Marshal(cs)
	if err != nil {
		return err
	}
	return rs.db.Cmd("SET", utils.CDR_STATS_PREFIX+cs.Id, marshaled).Err
}

func (rs *RedisStorage) GetCdrStats(key string) (cs *CdrStats, err error) {
	var values []byte
	if values, err = rs.db.Cmd("GET", utils.CDR_STATS_PREFIX+key).Bytes(); err == nil {
		err = rs.ms.Unmarshal(values, &cs)
	}
	return
}

func (rs *RedisStorage) GetAllCdrStats() (css []*CdrStats, err error) {
	conn, err := rs.db.Get()
	if err != nil {
		return nil, err
	}
	defer rs.db.Put(conn)
	keys, err := conn.Cmd("KEYS", utils.CDR_STATS_PREFIX+"*").List()
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		value, err := conn.Cmd("GET", key).Bytes()
		if err != nil {
			continue
		}
		cs := &CdrStats{}
		err = rs.ms.Unmarshal(value, cs)
		css = append(css, cs)
	}
	return
}

func (rs *RedisStorage) SetStructVersion(v *StructVersion) (err error) {
	var result []byte
	result, err = rs.ms.Marshal(v)
	if err != nil {
		return
	}
	return rs.db.Cmd("SET", utils.VERSION_PREFIX+"struct", result).Err
}

func (rs *RedisStorage) GetStructVersion() (rsv *StructVersion, err error) {
	var values []byte
	rsv = &StructVersion{}
	if values, err = rs.db.Cmd("GET", utils.VERSION_PREFIX+"struct").Bytes(); err == nil {
		err = rs.ms.Unmarshal(values, &rsv)
	}
	return
}

func (rs *RedisStorage) GetResourceLimit(id string, skipCache bool) (rl *ResourceLimit, err error) {
	key := utils.ResourceLimitsPrefix + id

	if !skipCache {
		if x, ok := cache2go.Get(key); ok {
			if x != nil {
				return x.(*ResourceLimit), nil
			}
			return nil, utils.ErrNotFound
		}
	}
	var values []byte
	if values, err = rs.db.Cmd("GET", key).Bytes(); err == nil {
		err = rs.ms.Unmarshal(values, &rl)
		for _, fltr := range rl.Filters {
			if err := fltr.CompileValues(); err != nil {
				return nil, err
			}
		}

		cache2go.Set(key, rl)
	}
	return
}
func (rs *RedisStorage) SetResourceLimit(rl *ResourceLimit) error {
	result, err := rs.ms.Marshal(rl)
	if err != nil {
		return err
	}
	key := utils.ResourceLimitsPrefix + rl.ID
	err = rs.db.Cmd("SET", key, result).Err
	//cache2go.Set(key, rl)
	return err
}
func (rs *RedisStorage) RemoveResourceLimit(id string) error {
	key := utils.ResourceLimitsPrefix + id
	if err := rs.db.Cmd("DEL", key).Err; err != nil {
		return err
	}
	cache2go.RemKey(key)
	return nil
}
