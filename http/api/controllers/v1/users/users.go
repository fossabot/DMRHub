package users

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/USA-RedDragon/dmrserver-in-a-box/http/api/apimodels"
	"github.com/USA-RedDragon/dmrserver-in-a-box/http/api/utils"
	"github.com/USA-RedDragon/dmrserver-in-a-box/models"
	"github.com/USA-RedDragon/dmrserver-in-a-box/userdb"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"k8s.io/klog/v2"
)

func GETUsers(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	users := models.ListUsers(db)
	c.JSON(http.StatusOK, users)
}

// Registration is JSON data from the frontend
func POSTUser(c *gin.Context) {
	db := c.MustGet("DB").(*gorm.DB)
	var json apimodels.UserRegistration
	err := c.ShouldBindJSON(&json)
	if err != nil {
		klog.Errorf("POSTUser: JSON data is invalid: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON data is invalid"})
	} else {
		registeredDMRID := false
		matchesCallsign := false
		// Check DMR ID is in the database
		userDB := *userdb.GetDMRUsers()
		for _, user := range userDB {
			if user.ID == json.DMRId {
				registeredDMRID = true
			}

			if strings.EqualFold(user.Callsign, json.Callsign) {
				matchesCallsign = true
				if registeredDMRID {
					break
				}
			}
			registeredDMRID = false
			matchesCallsign = false
		}
		if !registeredDMRID || !matchesCallsign {
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
		hashedPassword := utils.HashPassword(json.Password)

		// store the user in the database with Active = false
		user = models.User{
			Username: json.Username,
			Password: hashedPassword,
			Callsign: json.Callsign,
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
	user.Admin = true
	db.Save(&user)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User promoted"})
}

func POSTUserApprove(c *gin.Context) {
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
	db := c.MustGet("DB").(*gorm.DB)
	var users []models.User
	db.Preload("Repeaters").Find(&users, "admin = ?", true)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, users)
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
			user.Callsign = json.Callsign
		}

		if json.Username != "" {
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
			user.Username = json.Username
		}

		if json.Password != "" {
			user.Password = utils.HashPassword(json.Password)
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
	id := c.Param("id")
	db.Unscoped().Delete(&models.User{}, "id = ?", id)
	if db.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": db.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "User deleted"})
}

func POSTUserSuspend(c *gin.Context) {
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
	user.Approved = false
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

	userId := session.Get("user_id").(uint)
	if userId == 0 {
		klog.Error("userId not found")
		c.JSON(http.StatusUnauthorized, gin.H{"message": "Authentication failed"})
		return
	}

	user := models.FindUserByID(db, userId)
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