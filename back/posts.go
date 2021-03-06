package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/postgres"
)

////////////////////////////////////////////////////////////

// Post 投稿内容
type Post struct {
	/** 投稿ID */
	ID int64 `json:"id" gorm:"primary_key"`

	/** 投稿ユーザー */
	UserID int64 `json:"-" gorm:"primary_key" sql:"not null"`

	/** 投稿内容 */
	Text string `json:"text" sql:"not null"`

	/** 投稿時刻 */
	Timestamp int64 `json:"timestamp" sql:"not null"`

	/** ユーザーの投稿かを識別 */
	IsYours bool `json:"is_yours" gorm:"-"`

	/** 引用対象ID */
	QuoteID int64 `json:"quote_id"`
	/** 引用対象ポスト */
	QuotePost *Post `json:"quote_post" gorm:"ForeignKey:QuoteID;AssociationForeignKey:ID;"`

	/** 被Fav数 */
	FavoritedCount int64 `json:"favorited_count"`
}

// Fav お気に入り
type Fav struct {
	/** ユーザーID */
	UserID int64

	/** 投稿ID */
	PostID int64

	/** 投稿 */
	Post Post `gorm:"ForeignKey:PostID;AssociationForeignKey:ID;"`
}

////////////////////////////////////////////////////////////

func getQuotePost(post *Post) {
	if post.QuoteID != 0 {
		var quotePost Post
		db.Model(post).Related(&quotePost, "QuoteID")
		post.QuotePost = &quotePost
	}
}

////////////////////////////////////////////////////////////

/**
 * 指定keyのクエリパラメータを取得する
 */
func getQueryParam(r *http.Request, key string) (string, error) {
	params, ok := r.URL.Query()[key]

	if !ok || len(params[0]) < 1 {
		return "", fmt.Errorf("no valid parameters: \"%s\"", key)
	}

	return params[0], nil
}

/**
 * 簡易的なエラーチェック
 */
func check(w http.ResponseWriter, isError bool, msg string) bool {
	if isError {
		fmt.Printf("error: %s\n", msg)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(msg))
		return true
	}
	return false
}

////////////////////////////////////////////////////////////

/**
 * 投稿する
 *
 * POST /post?text=hoge&quote_id=1234
 *
 * params
 * - text: string (required)
 * - quote_id: int64 (optional, default = 0 (equals to nil))
 *
 * reqsponse: Post
 */
func postEndPoint(w http.ResponseWriter, r *http.Request, db *gorm.DB, user User) {
	var err error
	text, err := getQueryParam(r, "text")
	if check(w, err != nil, "posting failure") {
		return
	}

	var quoteID int64
	var quoteIDStr string
	quoteIDStr, err = getQueryParam(r, "quote_id")
	if err == nil {
		quoteID, err = strconv.ParseInt(quoteIDStr, 10, 64)
	}
	if err != nil {
		quoteID = 0
	}

	// DBに登録
	var post = Post{
		Text:      text,
		QuoteID:   quoteID,
		UserID:    user.ID,
		Timestamp: time.Now().Unix()}
	db.Create(&post)

	getQuotePost(&post)

	wsBroadcast <- WebSocketMessage{Type: CREATE, Post: post}

	post.IsYours = true

	// 結果を返す
	json.NewEncoder(w).Encode(post)
}

/**
 * 投稿を削除する
 *
 * DELETE /post?id=1234
 *
 * params
 * - id: int64 (required)
 *
 * response: 200(OK) or 400(BadRequest)
 */
func deletePostEndPoint(w http.ResponseWriter, r *http.Request, db *gorm.DB, user User) {
	postID, err := getQueryParam(r, "id")
	if check(w, err != nil, "deleting a post failure") {
		return
	}

	var post Post
	recordNotFound := db.Find(&post, "user_id = ? AND id = ?", user.ID, postID).RecordNotFound()
	if check(w, recordNotFound, "deleting a post failure") {
		return
	}

	// DBから削除
	// posts table
	db.Delete(Post{}, "id = ?", postID)
	// favs table
	db.Delete(Fav{}, "post_id = ?", postID)

	wsBroadcast <- WebSocketMessage{Type: DELETE, Post: post}

	// 結果を返す
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("deleting post succeeded\n"))
}

/**
 * 指定したIDの投稿を取得する
 *
 * GET /post?id=1234
 *
 * params
 * - id: int64 (required)
 *
 * response: 200(json of the post) or 400
 */
func getPostEndPoint(w http.ResponseWriter, r *http.Request, db *gorm.DB, user User) {
	postID, err := getQueryParam(r, "id")
	if check(w, err != nil, "favoriting a post failure") {
		return
	}

	var post Post
	if db.Find(&post, "id = ?", postID).RecordNotFound() {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(post)
	} else {
		w.WriteHeader(http.StatusBadRequest)
	}
}

/**
 * 最近の投稿を取得する
 *
 * GET /posts
 *
 * params
 * - limit: int32 (default = 20)
 * - offset: int32 (default = 0)
 *
 * response: []Post
 */
func getPostsEndPoint(w http.ResponseWriter, r *http.Request, db *gorm.DB, user User) {
	var limit, offset int64 = 20, 0

	limitStr, err := getQueryParam(r, "limit")
	if err == nil {
		limit, err = strconv.ParseInt(limitStr, 10, 32)
	}

	offsetStr, err := getQueryParam(r, "offset")
	if err == nil {
		offset, err = strconv.ParseInt(offsetStr, 10, 32)
	}

	var posts []Post
	db.Offset(offset).
		Limit(limit).
		Order("id desc").
		Find(&posts)

	for idx, p := range posts {
		posts[idx].IsYours = p.UserID == user.ID
		if !posts[idx].IsYours {
			posts[idx].FavoritedCount = 0
		}

		getQuotePost(&posts[idx])
	}

	json.NewEncoder(w).Encode(posts)
}

/**
 * ユーザーの最近の投稿を取得する
 *
 * GET /myposts
 *
 * params
 * - limit: int32 (default = 20)
 * - offset: int32 (default = 0)
 *
 * response: []Post
 */
func getMyPostsEndPoint(w http.ResponseWriter, r *http.Request, db *gorm.DB, user User) {
	var limit, offset int64 = 20, 0

	limitStr, err := getQueryParam(r, "limit")
	if err == nil {
		limit, err = strconv.ParseInt(limitStr, 10, 32)
	}

	offsetStr, err := getQueryParam(r, "offset")
	if err == nil {
		offset, err = strconv.ParseInt(offsetStr, 10, 32)
	}

	var posts []Post
	db.Where("user_id = ?", user.ID).
		Offset(offset).
		Limit(limit).
		Order("id desc").
		Find(&posts)

	for idx := range posts {
		posts[idx].IsYours = true
		getQuotePost(&posts[idx])
	}

	json.NewEncoder(w).Encode(posts)
}

/**
 * お気に入りの投稿を取得する
 *
 * GET /fav
 *
 * params
 * - limit: int32 (default = 20)
 * - offset: int32 (default = 0)
 *
 * response: []Post
 */
func getFavoritesEndPoint(w http.ResponseWriter, r *http.Request, db *gorm.DB, user User) {
	var limit, offset int64 = 20, 0

	limitStr, err := getQueryParam(r, "limit")
	if err == nil {
		limit, err = strconv.ParseInt(limitStr, 10, 32)
	}

	offsetStr, err := getQueryParam(r, "offset")
	if err == nil {
		offset, err = strconv.ParseInt(offsetStr, 10, 32)
	}

	var favs []Fav
	db.Where("user_id = ?", user.ID).
		Offset(offset).
		Limit(limit).
		Order("post_id desc").
		Select("DISTINCT(post_id)").
		Find(&favs)

	var posts []Post

	for _, fav := range favs {
		db.Model(&fav).Related(&fav.Post, "PostID")

		post := fav.Post
		post.IsYours = fav.Post.UserID == user.ID
		if !post.IsYours {
			post.FavoritedCount = 0
		}

		getQuotePost(&post)

		posts = append(posts, post)
	}

	json.NewEncoder(w).Encode(posts)
}

/**
 * 投稿をお気に入りにする
 *
 * POST /fav?id=1234
 *
 * params
 * - id: int64 (required)
 */
func favoritePostEndPoint(w http.ResponseWriter, r *http.Request, db *gorm.DB, user User) {
	postID, err := getQueryParam(r, "id")
	if check(w, err != nil, "favoriting a post failure") {
		return
	}

	var post Post
	recordNotFound := db.Find(&post, "id = ?", postID).RecordNotFound()
	if check(w, recordNotFound, "favoriting a post failure") {
		return
	}

	var fav Fav
	fav.UserID = user.ID
	fav.PostID = post.ID

	db.Model(&post).UpdateColumn("FavoritedCount", post.FavoritedCount+1)
	db.Create(&fav)

	getQuotePost(&post)

	wsBroadcast <- WebSocketMessage{Type: UPDATE, Post: post}
}
