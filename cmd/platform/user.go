// Copyright (c) 2016-present Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.
package main

import (
	"errors"
	"fmt"

	"github.com/primefour/servers/app"
	"github.com/primefour/servers/einterfaces"
	"github.com/primefour/servers/model"
	"github.com/primefour/servers/utils"
	"github.com/spf13/cobra"
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Management of users",
}

var userActivateCmd = &cobra.Command{
	Use:   "activate [emails, usernames, userIds]",
	Short: "Activate users",
	Long:  "Activate users that have been deactivated.",
	Example: `  user activate user@example.com
  user activate username`,
	RunE: userActivateCmdF,
}

var userDeactivateCmd = &cobra.Command{
	Use:   "deactivate [emails, usernames, userIds]",
	Short: "Deactivate users",
	Long:  "Deactivate users. Deactivated users are immediately logged out of all sessions and are unable to log back in.",
	Example: `  user deactivate user@example.com
  user deactivate username`,
	RunE: userDeactivateCmdF,
}

var userCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a user",
	Long:  "Create a user",
	Example: `  user create --email user@example.com --username userexample --password Password1
  user create --firstname Joe --system_admin --email joe@example.com --username joe --password Password1`,
	RunE: userCreateCmdF,
}

var userInviteCmd = &cobra.Command{
	Use:   "invite [email] [teams]",
	Short: "Send user an email invite to a team.",
	Long: `Send user an email invite to a team.
You can invite a user to multiple teams by listing them.
You can specify teams by name or ID.`,
	Example: `  user invite user@example.com myteam
  user invite user@example.com myteam1 myteam2`,
	RunE: userInviteCmdF,
}

var resetUserPasswordCmd = &cobra.Command{
	Use:     "password [user] [password]",
	Short:   "Set a user's password",
	Long:    "Set a user's password",
	Example: "  user password user@example.com Password1",
	RunE:    resetUserPasswordCmdF,
}

var resetUserMfaCmd = &cobra.Command{
	Use:   "resetmfa [users]",
	Short: "Turn off MFA",
	Long: `Turn off multi-factor authentication for a user.
If MFA enforcement is enabled, the user will be forced to re-enable MFA as soon as they login.`,
	Example: "  user resetmfa user@example.com",
	RunE:    resetUserMfaCmdF,
}

var deleteUserCmd = &cobra.Command{
	Use:     "delete [users]",
	Short:   "Delete users and all posts",
	Long:    "Permanently delete user and all related information including posts.",
	Example: "  user delete user@example.com",
	RunE:    deleteUserCmdF,
}

var deleteAllUsersCmd = &cobra.Command{
	Use:     "deleteall",
	Short:   "Delete all users and all posts",
	Long:    "Permanently delete all users and all related information including posts.",
	Example: "  user deleteall",
	RunE:    deleteUserCmdF,
}

var migrateAuthCmd = &cobra.Command{
	Use:   "migrate_auth [from_auth] [to_auth] [match_field]",
	Short: "Mass migrate user accounts authentication type",
	Long: `Migrates accounts from one authentication provider to another. For example, you can upgrade your authentication provider from email to ldap.

from_auth:
	The authentication service to migrate users accounts from.
	Supported options: email, gitlab, saml.

to_auth:
	The authentication service to migrate users to.
	Supported options: ldap.

match_field:
	The field that is guaranteed to be the same in both authentication services. For example, if the users emails are consistent set to email.
	Supported options: email, username.

Will display any accounts that are not migrated successfully.`,
	Example: "  user migrate_auth email ladp email",
	RunE:    migrateAuthCmdF,
}

var verifyUserCmd = &cobra.Command{
	Use:     "verify [users]",
	Short:   "Verify email of users",
	Long:    "Verify the emails of some users.",
	Example: "  user verify user1",
	RunE:    verifyUserCmdF,
}

var searchUserCmd = &cobra.Command{
	Use:     "search [users]",
	Short:   "Search for users",
	Long:    "Search for users based on username, email, or user ID.",
	Example: "  user search user1@mail.com user2@mail.com",
	RunE:    searchUserCmdF,
}

func init() {
	userCreateCmd.Flags().String("username", "", "Username")
	userCreateCmd.Flags().String("email", "", "Email")
	userCreateCmd.Flags().String("password", "", "Password")
	userCreateCmd.Flags().String("nickname", "", "Nickname")
	userCreateCmd.Flags().String("firstname", "", "First Name")
	userCreateCmd.Flags().String("lastname", "", "Last Name")
	userCreateCmd.Flags().String("locale", "", "Locale (ex: en, fr)")
	userCreateCmd.Flags().Bool("system_admin", false, "Make the user a system administrator")

	deleteUserCmd.Flags().Bool("confirm", false, "Confirm you really want to delete the user and a DB backup has been performed.")

	deleteAllUsersCmd.Flags().Bool("confirm", false, "Confirm you really want to delete the user and a DB backup has been performed.")

	userCmd.AddCommand(
		userActivateCmd,
		userDeactivateCmd,
		userCreateCmd,
		userInviteCmd,
		resetUserPasswordCmd,
		resetUserMfaCmd,
		deleteUserCmd,
		deleteAllUsersCmd,
		migrateAuthCmd,
		verifyUserCmd,
		searchUserCmd,
	)
}

func userActivateCmdF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)

	if len(args) < 1 {
		return errors.New("Enter user(s) to activate.")
	}

	changeUsersActiveStatus(args, true)
	return nil
}

func changeUsersActiveStatus(userArgs []string, active bool) {
	users := getUsersFromUserArgs(userArgs)
	for i, user := range users {
		err := changeUserActiveStatus(user, userArgs[i], active)

		if err != nil {
			CommandPrintErrorln(err.Error())
		}
	}
}

func changeUserActiveStatus(user *model.User, userArg string, activate bool) error {
	if user == nil {
		return fmt.Errorf("Can't find user '%v'", userArg)
	}
	if user.IsLDAPUser() {
		return errors.New(utils.T("api.user.update_active.no_deactivate_ldap.app_error"))
	}
	if _, err := app.UpdateActive(user, activate); err != nil {
		return fmt.Errorf("Unable to change activation status of user: %v", userArg)
	}

	return nil
}

func userDeactivateCmdF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)

	if len(args) < 1 {
		return errors.New("Enter user(s) to deactivate.")
	}

	changeUsersActiveStatus(args, false)
	return nil
}

func userCreateCmdF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)
	username, erru := cmd.Flags().GetString("username")
	if erru != nil || username == "" {
		return errors.New("Username is required")
	}
	email, erre := cmd.Flags().GetString("email")
	if erre != nil || email == "" {
		return errors.New("Email is required")
	}
	password, errp := cmd.Flags().GetString("password")
	if errp != nil || password == "" {
		return errors.New("Password is required")
	}
	nickname, _ := cmd.Flags().GetString("nickname")
	firstname, _ := cmd.Flags().GetString("firstname")
	lastname, _ := cmd.Flags().GetString("lastname")
	locale, _ := cmd.Flags().GetString("locale")
	system_admin, _ := cmd.Flags().GetBool("system_admin")

	user := &model.User{
		Username:  username,
		Email:     email,
		Password:  password,
		Nickname:  nickname,
		FirstName: firstname,
		LastName:  lastname,
		Locale:    locale,
	}

	ruser, err := app.CreateUser(user)
	if err != nil {
		return errors.New("Unable to create user. Error: " + err.Error())
	}

	if system_admin {
		app.UpdateUserRoles(ruser.Id, "system_user system_admin")
	}

	CommandPrettyPrintln("Created User")

	return nil
}

func userInviteCmdF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)
	utils.InitHTML()

	if len(args) < 2 {
		return errors.New("Not enough arguments.")
	}

	email := args[0]
	if !model.IsValidEmail(email) {
		return errors.New("Invalid email")
	}

	teams := getTeamsFromTeamArgs(args[1:])
	for i, team := range teams {
		err := inviteUser(email, team, args[i+1])

		if err != nil {
			CommandPrintErrorln(err.Error())
		}
	}

	return nil
}

func inviteUser(email string, team *model.Team, teamArg string) error {
	invites := []string{email}
	if team == nil {
		return fmt.Errorf("Can't find team '%v'", teamArg)
	}

	app.SendInviteEmails(team, "Administrator", invites, *utils.Cfg.ServiceSettings.SiteURL)
	CommandPrettyPrintln("Invites may or may not have been sent.")

	return nil
}

func resetUserPasswordCmdF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)
	if len(args) != 2 {
		return errors.New("Incorect number of arguments.")
	}

	user := getUserFromUserArg(args[0])
	if user == nil {
		return errors.New("Unable to find user '" + args[0] + "'")
	}
	password := args[1]

	if result := <-app.Srv.Store.User().UpdatePassword(user.Id, model.HashPassword(password)); result.Err != nil {
		return result.Err
	}

	return nil
}

func resetUserMfaCmdF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)
	if len(args) < 1 {
		return errors.New("Enter at least one user.")
	}

	users := getUsersFromUserArgs(args)

	for i, user := range users {
		if user == nil {
			return errors.New("Unable to find user '" + args[i] + "'")
		}

		if err := app.DeactivateMfa(user.Id); err != nil {
			return err
		}
	}

	return nil
}

func deleteUserCmdF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)
	if len(args) < 1 {
		return errors.New("Enter at least one user.")
	}

	confirmFlag, _ := cmd.Flags().GetBool("confirm")
	if !confirmFlag {
		var confirm string
		CommandPrettyPrintln("Have you performed a database backup? (YES/NO): ")
		fmt.Scanln(&confirm)

		if confirm != "YES" {
			return errors.New("ABORTED: You did not answer YES exactly, in all capitals.")
		}
		CommandPrettyPrintln("Are you sure you want to delete the teams specified?  All data will be permanently deleted? (YES/NO): ")
		fmt.Scanln(&confirm)
		if confirm != "YES" {
			return errors.New("ABORTED: You did not answer YES exactly, in all capitals.")
		}
	}

	users := getUsersFromUserArgs(args)

	for i, user := range users {
		if user == nil {
			return errors.New("Unable to find user '" + args[i] + "'")
		}

		if err := app.PermanentDeleteUser(user); err != nil {
			return err
		}
	}

	return nil
}

func deleteAllUsersCommandF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)
	if len(args) > 0 {
		return errors.New("Don't enter any agruments.")
	}

	confirmFlag, _ := cmd.Flags().GetBool("confirm")
	if !confirmFlag {
		var confirm string
		CommandPrettyPrintln("Have you performed a database backup? (YES/NO): ")
		fmt.Scanln(&confirm)

		if confirm != "YES" {
			return errors.New("ABORTED: You did not answer YES exactly, in all capitals.")
		}
		CommandPrettyPrintln("Are you sure you want to delete the teams specified?  All data will be permanently deleted? (YES/NO): ")
		fmt.Scanln(&confirm)
		if confirm != "YES" {
			return errors.New("ABORTED: You did not answer YES exactly, in all capitals.")
		}
	}

	if err := app.PermanentDeleteAllUsers(); err != nil {
		return err
	} else {
		CommandPrettyPrintln("Sucsessfull. All users deleted.")
	}

	return nil
}

func migrateAuthCmdF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)
	if len(args) != 3 {
		return errors.New("Enter the correct number of arguments.")
	}

	fromAuth := args[0]
	toAuth := args[1]
	matchField := args[2]

	if len(fromAuth) == 0 || (fromAuth != "email" && fromAuth != "gitlab" && fromAuth != "saml") {
		return errors.New("Invalid from_auth argument")
	}

	if len(toAuth) == 0 || toAuth != "ldap" {
		return errors.New("Invalid to_auth argument")
	}

	// Email auth in Mattermost system is represented by ""
	if fromAuth == "email" {
		fromAuth = ""
	}

	if len(matchField) == 0 || (matchField != "email" && matchField != "username") {
		return errors.New("Invalid match_field argument")
	}

	if migrate := einterfaces.GetAccountMigrationInterface(); migrate != nil {
		if err := migrate.MigrateToLdap(fromAuth, matchField); err != nil {
			return errors.New("Error while migrating users: " + err.Error())
		} else {
			CommandPrettyPrintln("Sucessfully migrated accounts.")
		}
	}

	return nil
}

func verifyUserCmdF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)
	if len(args) < 1 {
		return errors.New("Enter at least one user.")
	}

	users := getUsersFromUserArgs(args)

	for i, user := range users {
		if user == nil {
			CommandPrintErrorln("Unable to find user '" + args[i] + "'")
			continue
		}
		if cresult := <-app.Srv.Store.User().VerifyEmail(user.Id); cresult.Err != nil {
			CommandPrintErrorln("Unable to verify '" + args[i] + "' email. Error: " + cresult.Err.Error())
		}
	}

	return nil
}

func searchUserCmdF(cmd *cobra.Command, args []string) error {
	initDBCommandContextCobra(cmd)
	if len(args) < 1 {
		return errors.New("Enter at least one query.")
	}

	users := getUsersFromUserArgs(args)

	for i, user := range users {
		if i > 0 {
			CommandPrettyPrintln("------------------------------")
		}
		if user == nil {
			CommandPrintErrorln("Unable to find user '" + args[i] + "'")
			continue
		}

		CommandPrettyPrintln("id: " + user.Id)
		CommandPrettyPrintln("username: " + user.Username)
		CommandPrettyPrintln("nickname: " + user.Nickname)
		CommandPrettyPrintln("position: " + user.Position)
		CommandPrettyPrintln("first_name: " + user.FirstName)
		CommandPrettyPrintln("last_name: " + user.LastName)
		CommandPrettyPrintln("email: " + user.Email)
		CommandPrettyPrintln("auth_service: " + user.AuthService)
	}

	return nil
}
