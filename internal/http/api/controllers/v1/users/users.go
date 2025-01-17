package users

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"crypto/sha1" //#nosec G505 -- False positive, we are not using this for crypto, just HIBP

	"github.com/USA-RedDragon/DMRHub/internal/config"
	"github.com/USA-RedDragon/DMRHub/internal/http/api/apimodels"
	"github.com/USA-RedDragon/DMRHub/internal/http/api/utils"
	"github.com/USA-RedDragon/DMRHub/internal/models"
	"github.com/USA-RedDragon/DMRHub/internal/userdb"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	gopwned "github.com/mavjs/goPwned"
	"gorm.io/gorm"
	"k8s.io/klog/v2"
)

func GETUsers(c *gin.Context) {
	db := c.MustGet("PaginatedDB").(*gorm.DB)
	cDb := c.MustGet("DB").(*gorm.DB)
	users := models.ListUsers(db)
	total := models.CountUsers(cDb)
	c.JSON(http.StatusOK, gin.H{"total": total, "users": users})
}

// POSTUser is used to register a new user
func POSTUser(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	var json apimodels.UserRegistration
	err := c.ShouldBindJSON(&json)
	if err != nil {
		klog.Errorf("POSTUser: JSON data is invalid: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON data is invalid"})
	} else {
		if !userdb.IsValidUserID(json.DMRId) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "DMR ID is not valid"})
			return
		}
		if !userdb.IsInDB(json.DMRId, json.Callsign) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "DMR ID is not registered or Callsign does not match"})
			return
		}
		isValid, errString := json.IsValidUsername()
		if !isValid {
			c.JSON(http.StatusBadRequest, gin.H{"error": errString})
			return
		}

		// Check that password isn't a zero string
		if json.Password == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Password cannot be blank"})
			return
		}

		if config.GetConfig().HIBPAPIKey != "" {
			goPwned := gopwned.NewClient(nil, config.GetConfig().HIBPAPIKey)
			h := sha1.New() //#nosec G401 -- False positive, we are not using this for crypto, just HIBP
			h.Write([]byte(json.Password))
			sha1HashedPW := fmt.Sprintf("%X", h.Sum(nil))
			frange := sha1HashedPW[0:5]
			lrange := sha1HashedPW[5:40]
			karray, err := goPwned.GetPwnedPasswords(frange, false)
			if err != nil {
				// If the error message starts with "Too many requests", then tell the user to retry in one minute
				if strings.HasPrefix(err.Error(), "Too many requests") {
					c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests. Please try again in one minute"})
					return
				}
				klog.Errorf("POSTUser: Error getting pwned passwords: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Error getting pwned passwords"})
				return
			}
			strKArray := string(karray)
			respArray := strings.Split(strKArray, "\r\n")

			var result int64
			for _, resp := range respArray {
				strArray := strings.Split(resp, ":")
				test := strArray[0]

				count, err := strconv.ParseInt(strArray[1], 0, 32)
				if err != nil {
					klog.Errorf("POSTUser: Error parsing pwned password count: %v", err)
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Error parsing pwned password count"})
					return
				}
				if test == lrange {
					result = count
				}
			}
			if result > 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Password has been reported in a data breach. Please use another one"})
				return
			}
		}

		// Check if the username is already taken
		var user models.User
		db.Find(&user, "username = ?", json.Username)
		if db.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
			return
		} else if user.ID != 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Username is already taken"})
			return
		}

		// argon2 the password
		hashedPassword := utils.HashPassword(json.Password, config.GetConfig().PasswordSalt)

		// store the user in the database with Active = false
		user = models.User{
			Username: json.Username,
			Password: hashedPassword,
			Callsign: strings.ToUpper(json.Callsign),
			ID:       json.DMRId,
			Approved: false,
			Admin:    false,
		}
		db.Create(&user)
		if db.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User created, please wait for admin approval"})
	}
}

func POSTUserDemote(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	id := c.Param("id")

	userID, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid User ID"})
		return
	}
	session := sessions.Default(c)
	fromUserID := session.Get("user_id").(uint)
	if uint(userID) == fromUserID {
		// don't allow a user to demote themselves
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot demote yourself"})
		return
	}
	// Grab the user from the database
	var user models.User
	db.Find(&user, "id = ?", id)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	if user.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User does not exist"})
		return
	}
	user.Admin = false
	db.Save(&user)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User demoted"})
}

func POSTUserPromote(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	id := c.Param("id")

	// Grab the user from the database
	var user models.User
	db.Find(&user, "id = ?", id)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	if user.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User does not exist"})
		return
	}
	if user.ID == 9990 {
		// Prevent promoting the Parrot user
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot promote the Parrot user"})
		return
	}
	if !user.Approved {
		// Prevent promoting an unapproved user
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot promote an unapproved user"})
		return
	}
	user.Admin = true
	db.Save(&user)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User promoted"})
}

func POSTUserUnsuspend(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	id := c.Param("id")

	userID, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid User ID"})
		return
	}
	session := sessions.Default(c)
	fromUserID := session.Get("user_id").(uint)
	if uint(userID) == fromUserID {
		// don't allow a user to demote themselves
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot unsuspend yourself"})
		return
	}

	// Grab the user from the database
	var user models.User
	db.Find(&user, "id = ?", id)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	if user.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User does not exist"})
		return
	}
	user.Suspended = false
	db.Save(&user)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User unsuspended"})
}

func POSTUserApprove(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	id := c.Param("id")

	userID, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid User ID"})
		return
	}
	session := sessions.Default(c)
	fromUserID := session.Get("user_id").(uint)
	if uint(userID) == fromUserID {
		// don't allow a user to demote themselves
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot approve yourself"})
		return
	}

	// Grab the user from the database
	var user models.User
	db.Find(&user, "id = ?", id)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	if user.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User does not exist"})
		return
	}
	user.Approved = true
	db.Save(&user)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User approved"})
}

func GETUser(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	id := c.Param("id")
	// Convert string id into uint
	userID, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid User ID"})
		return
	}
	if models.UserIDExists(db, uint(userID)) {
		user := models.FindUserByID(db, uint(userID))
		c.JSON(http.StatusOK, user)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User does not exist"})
	}
}

func GETUserAdmins(c *gin.Context) {
	db := c.MustGet("PaginatedDB").(*gorm.DB)
	cDb := c.MustGet("DB").(*gorm.DB)

	users := models.FindUserAdmins(db)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	total := models.CountUserAdmins(cDb)
	c.JSON(http.StatusOK, gin.H{"users": users, "total": total})
}

func GETUserSuspended(c *gin.Context) {
	db := c.MustGet("PaginatedDB").(*gorm.DB)
	cDb := c.MustGet("DB").(*gorm.DB)
	// Get all users where approved = false
	users := models.FindUserSuspended(db)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	total := models.CountUserSuspended(cDb)
	c.JSON(http.StatusOK, gin.H{"users": users, "total": total})
}

func GETUserUnapproved(c *gin.Context) {
	db := c.MustGet("PaginatedDB").(*gorm.DB)
	cDb := c.MustGet("DB").(*gorm.DB)
	// Get all users where approved = false
	users := models.FindUserUnapproved(db)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	total := models.CountUserUnapproved(cDb)
	c.JSON(http.StatusOK, gin.H{"users": users, "total": total})
}

func PATCHUser(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	id := c.Param("id")
	var json apimodels.UserPatch
	err := c.ShouldBindJSON(&json)
	if err != nil {
		klog.Errorf("PATCHUser: JSON data is invalid: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON data is invalid"})
	} else {
		// Update callsign, username, and/or password
		var user models.User
		db.Find(&user, "id = ?", id)
		if db.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
			return
		}
		if user.ID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "User does not exist"})
			return
		}
		if json.Callsign != "" {
			matchesCallsign := false
			// Check DMR ID is in the database
			userDB := *userdb.GetDMRUsers()
			for _, user := range userDB {
				if fmt.Sprintf("%d", user.ID) == id && strings.EqualFold(user.Callsign, json.Callsign) {
					matchesCallsign = true
					break
				}
			}
			if !matchesCallsign {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Callsign does not match DMR ID"})
				return
			}
			user.Callsign = strings.ToUpper(json.Callsign)
		}

		if json.Username != "" {
			// Check if the username is already taken
			var existingUser models.User
			db.Find(&existingUser, "username = ?", json.Username)
			if db.Error != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
				return
			} else if existingUser.ID != 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Username is already taken"})
				return
			}
			user.Username = json.Username
		}

		if json.Password != "" {
			user.Password = utils.HashPassword(json.Password, config.GetConfig().PasswordSalt)
		}

		db.Save(&user)
		if db.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "User updated"})
	}
}

func DELETEUser(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	idUint64, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}
	models.DeleteUser(db, uint(idUint64))
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User deleted"})
}

func POSTUserSuspend(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	id := c.Param("id")

	userID, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid User ID"})
		return
	}
	session := sessions.Default(c)
	fromUserID := session.Get("user_id").(uint)
	if uint(userID) == fromUserID {
		// don't allow a user to demote themselves
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot suspend yourself"})
		return
	}

	// Grab the user from the database
	var user models.User
	db.Find(&user, "id = ?", id)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	if user.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User does not exist"})
		return
	}

	if user.Admin || user.ID == 999999 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot suspend an admin"})
		return
	}

	if user.ID == 9990 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You cannot suspend the Parrot user"})
		return
	}

	user.Suspended = true
	db.Save(&user)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User suspended"})
}

func GETUserSelf(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	session := sessions.Default(c)

	userID := session.Get("user_id")
	if userID == nil {
		klog.Error("userID not found")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication failed"})
		return
	}

	user := models.FindUserByID(db, userID.(uint))
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	if user.ID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User does not exist"})
		return
	}
	c.JSON(http.StatusOK, user)
}
