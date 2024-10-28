package logic

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gravitl/netmaker/database"
	"github.com/gravitl/netmaker/logic"
	"github.com/gravitl/netmaker/models"
	"github.com/gravitl/netmaker/mq"
	"github.com/gravitl/netmaker/servercfg"
	"golang.org/x/exp/slog"
)

var ServiceUserPermissionTemplate = models.UserRolePermissionTemplate{
	ID:                  models.ServiceUser,
	UiName:              "Network User",
	Default:             true,
	FullAccess:          false,
	DenyDashboardAccess: true,
}

var PlatformUserUserPermissionTemplate = models.UserRolePermissionTemplate{
	ID:         models.PlatformUser,
	UiName:     "Network Admin",
	Default:    true,
	FullAccess: false,
}

func UserRolesInit() {
	d, _ := json.Marshal(logic.SuperAdminPermissionTemplate)
	database.Insert(logic.SuperAdminPermissionTemplate.ID.String(), string(d), database.USER_PERMISSIONS_TABLE_NAME)
	d, _ = json.Marshal(logic.AdminPermissionTemplate)
	database.Insert(logic.AdminPermissionTemplate.ID.String(), string(d), database.USER_PERMISSIONS_TABLE_NAME)
	d, _ = json.Marshal(ServiceUserPermissionTemplate)
	database.Insert(ServiceUserPermissionTemplate.ID.String(), string(d), database.USER_PERMISSIONS_TABLE_NAME)
	d, _ = json.Marshal(PlatformUserUserPermissionTemplate)
	database.Insert(PlatformUserUserPermissionTemplate.ID.String(), string(d), database.USER_PERMISSIONS_TABLE_NAME)

}

func ValidateCreateRoleReq(userRole *models.UserRolePermissionTemplate) error {
	// check if role exists with this id
	_, err := logic.GetRole(userRole.ID)
	if err == nil {
		return fmt.Errorf("role with id `%s` exists already", userRole.ID.String())
	}
	if len(userRole.NetworkLevelAccess) > 0 {
		for rsrcType := range userRole.NetworkLevelAccess {
			if _, ok := models.RsrcTypeMap[rsrcType]; !ok {
				return errors.New("invalid rsrc type " + rsrcType.String())
			}
			if rsrcType == models.RemoteAccessGwRsrc {
				userRsrcPermissions := userRole.NetworkLevelAccess[models.RemoteAccessGwRsrc]
				var vpnAccess bool
				for _, scope := range userRsrcPermissions {
					if scope.VPNaccess {
						vpnAccess = true
						break
					}
				}
				if vpnAccess {
					userRole.NetworkLevelAccess[models.ExtClientsRsrc] = map[models.RsrcID]models.RsrcPermissionScope{
						models.AllExtClientsRsrcID: {
							Read:     true,
							Create:   true,
							Update:   true,
							Delete:   true,
							SelfOnly: true,
						},
					}

				}

			}
		}
	}
	return nil
}

func ValidateUpdateRoleReq(userRole *models.UserRolePermissionTemplate) error {
	roleInDB, err := logic.GetRole(userRole.ID)
	if err != nil {
		return err
	}

	if roleInDB.Default {
		return errors.New("cannot update default role")
	}
	if len(userRole.NetworkLevelAccess) > 0 {
		for rsrcType := range userRole.NetworkLevelAccess {
			if _, ok := models.RsrcTypeMap[rsrcType]; !ok {
				return errors.New("invalid rsrc type " + rsrcType.String())
			}
			if rsrcType == models.RemoteAccessGwRsrc {
				userRsrcPermissions := userRole.NetworkLevelAccess[models.RemoteAccessGwRsrc]
				var vpnAccess bool
				for _, scope := range userRsrcPermissions {
					if scope.VPNaccess {
						vpnAccess = true
						break
					}
				}
				if vpnAccess {
					userRole.NetworkLevelAccess[models.ExtClientsRsrc] = map[models.RsrcID]models.RsrcPermissionScope{
						models.AllExtClientsRsrcID: {
							Read:     true,
							Create:   true,
							Update:   true,
							Delete:   true,
							SelfOnly: true,
						},
					}

				}

			}
		}
	}
	return nil
}

// CreateRole - inserts new role into DB
func CreateRole(r models.UserRolePermissionTemplate) error {
	// check if role already exists
	if r.ID.String() == "" {
		return errors.New("role id cannot be empty")
	}
	_, err := database.FetchRecord(database.USER_PERMISSIONS_TABLE_NAME, r.ID.String())
	if err == nil {
		return errors.New("role already exists")
	}
	d, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return database.Insert(r.ID.String(), string(d), database.USER_PERMISSIONS_TABLE_NAME)
}

// UpdateRole - updates role template
func UpdateRole(r models.UserRolePermissionTemplate) error {
	if r.ID.String() == "" {
		return errors.New("role id cannot be empty")
	}
	_, err := database.FetchRecord(database.USER_PERMISSIONS_TABLE_NAME, r.ID.String())
	if err != nil {
		return err
	}
	d, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return database.Insert(r.ID.String(), string(d), database.USER_PERMISSIONS_TABLE_NAME)
}

// DeleteRole - deletes user role
func DeleteRole(rid models.UserRoleID, force bool) error {
	if rid.String() == "" {
		return errors.New("role id cannot be empty")
	}
	users, err := logic.GetUsersDB()
	if err != nil {
		return err
	}
	role, err := logic.GetRole(rid)
	if err != nil {
		return err
	}

	for _, user := range users {
		for userG := range user.UserGroups {
			_, err := GetUserGroup(userG)
			if err == nil {

			}
		}

		if user.PlatformRoleID == rid {
			err = errors.New("active roles cannot be deleted.switch existing users to a new role before deleting")
			return err
		}

	}
	return database.DeleteRecord(database.USER_PERMISSIONS_TABLE_NAME, role.ID.String())
}

func ValidateCreateGroupReq(g models.UserGroup) error {

	// check if network roles are valid
	for _, roleMap := range g.NetworkRoles {
		for roleID := range roleMap {
			_, err := logic.GetRole(roleID)
			if err != nil {
				return fmt.Errorf("invalid network role %s", roleID)
			}

		}
	}
	return nil
}
func ValidateUpdateGroupReq(g models.UserGroup) error {

	for networkID := range g.NetworkRoles {
		userRolesMap := g.NetworkRoles[networkID]
		for roleID := range userRolesMap {
			_, err := logic.GetRole(roleID)
			if err != nil {
				err = fmt.Errorf("invalid network role")
				return err
			}
		}
	}
	return nil
}

// CreateUserGroup - creates new user group
func CreateUserGroup(g models.UserGroup) error {
	// check if role already exists
	if g.ID == "" {
		return errors.New("group id cannot be empty")
	}
	_, err := database.FetchRecord(database.USER_GROUPS_TABLE_NAME, g.ID.String())
	if err == nil {
		return errors.New("group already exists")
	}
	d, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return database.Insert(g.ID.String(), string(d), database.USER_GROUPS_TABLE_NAME)
}

// GetUserGroup - fetches user group
func GetUserGroup(gid models.UserGroupID) (models.UserGroup, error) {
	d, err := database.FetchRecord(database.USER_GROUPS_TABLE_NAME, gid.String())
	if err != nil {
		return models.UserGroup{}, err
	}
	var ug models.UserGroup
	err = json.Unmarshal([]byte(d), &ug)
	if err != nil {
		return ug, err
	}
	return ug, nil
}

// ListUserGroups - lists user groups
func ListUserGroups() ([]models.UserGroup, error) {
	data, err := database.FetchRecords(database.USER_GROUPS_TABLE_NAME)
	if err != nil && !database.IsEmptyRecord(err) {
		return []models.UserGroup{}, err
	}
	userGroups := []models.UserGroup{}
	for _, dataI := range data {
		userGroup := models.UserGroup{}
		err := json.Unmarshal([]byte(dataI), &userGroup)
		if err != nil {
			continue
		}
		userGroups = append(userGroups, userGroup)
	}
	return userGroups, nil
}

// UpdateUserGroup - updates new user group
func UpdateUserGroup(g models.UserGroup) error {
	// check if group exists
	if g.ID == "" {
		return errors.New("group id cannot be empty")
	}
	_, err := database.FetchRecord(database.USER_GROUPS_TABLE_NAME, g.ID.String())
	if err != nil {
		return err
	}
	d, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return database.Insert(g.ID.String(), string(d), database.USER_GROUPS_TABLE_NAME)
}

// DeleteUserGroup - deletes user group
func DeleteUserGroup(gid models.UserGroupID) error {
	users, err := logic.GetUsersDB()
	if err != nil {
		return err
	}
	for _, user := range users {
		delete(user.UserGroups, gid)
		logic.UpsertUser(user)
	}
	return database.DeleteRecord(database.USER_GROUPS_TABLE_NAME, gid.String())
}

func HasNetworkRsrcScope(permissionTemplate models.UserRolePermissionTemplate, netid string, rsrcType models.RsrcType, rsrcID models.RsrcID, op string) bool {
	if permissionTemplate.FullAccess {
		return true
	}

	rsrcScope, ok := permissionTemplate.NetworkLevelAccess[rsrcType]
	if !ok {
		return false
	}
	_, ok = rsrcScope[rsrcID]
	return ok
}

func GetUserRAGNodesV1(user models.User) (gws map[string]models.Node) {
	gws = make(map[string]models.Node)
	nodes, err := logic.GetAllNodes()
	if err != nil {
		return
	}
	if user.IsAdmin || user.IsSuperAdmin {
		for _, node := range nodes {
			if node.IsIngressGateway {
				gws[node.ID.String()] = node
			}

		}
	}
	tagNodesMap := logic.GetTagMapWithNodes()
	accessPolices := logic.ListUserPolicies(user)
	for _, policyI := range accessPolices {
		if !policyI.Enabled {
			continue
		}
		for _, dstI := range policyI.Dst {
			if dstI.Value == "*" {
				networkNodes := logic.GetNetworkNodesMemory(nodes, policyI.NetworkID.String())
				for _, node := range networkNodes {
					if node.IsIngressGateway {
						gws[node.ID.String()] = node
					}
				}
			}
			if nodes, ok := tagNodesMap[models.TagID(dstI.Value)]; ok {
				for _, node := range nodes {
					if node.IsIngressGateway {
						gws[node.ID.String()] = node
					}

				}
			}
		}
	}
	return
}

func GetFilteredNodesByUserAccess(user models.User, nodes []models.Node) (filteredNodes []models.Node) {

	nodesMap := make(map[string]struct{})
	allNetworkRoles := make(map[models.UserRoleID]struct{})
	defer func() {
		filteredNodes = logic.AddStaticNodestoList(filteredNodes)
	}()

	if len(user.UserGroups) > 0 {
		for userGID := range user.UserGroups {
			userG, err := GetUserGroup(userGID)
			if err == nil {
				if len(userG.NetworkRoles) > 0 {
					if _, ok := userG.NetworkRoles[models.AllNetworks]; ok {
						filteredNodes = nodes
						return
					}
					for _, netRoles := range userG.NetworkRoles {
						for netRoleI := range netRoles {
							allNetworkRoles[netRoleI] = struct{}{}
						}
					}
				}
			}
		}
	}
	for networkRoleID := range allNetworkRoles {
		userPermTemplate, err := logic.GetRole(networkRoleID)
		if err != nil {
			continue
		}
		// TODO: MIGRATE
		networkNodes := logic.GetNetworkNodesMemory(nodes, "netmaker")
		if userPermTemplate.FullAccess {
			for _, node := range networkNodes {
				if _, ok := nodesMap[node.ID.String()]; ok {
					continue
				}
				nodesMap[node.ID.String()] = struct{}{}
				filteredNodes = append(filteredNodes, node)
			}

			continue
		}
		if rsrcPerms, ok := userPermTemplate.NetworkLevelAccess[models.RemoteAccessGwRsrc]; ok {
			if _, ok := rsrcPerms[models.AllRemoteAccessGwRsrcID]; ok {
				for _, node := range networkNodes {
					if _, ok := nodesMap[node.ID.String()]; ok {
						continue
					}
					if node.IsIngressGateway {
						nodesMap[node.ID.String()] = struct{}{}
						filteredNodes = append(filteredNodes, node)
					}
				}
			} else {
				for gwID, scope := range rsrcPerms {
					if _, ok := nodesMap[gwID.String()]; ok {
						continue
					}
					if scope.Read {
						gwNode, err := logic.GetNodeByID(gwID.String())
						if err == nil && gwNode.IsIngressGateway {
							nodesMap[gwNode.ID.String()] = struct{}{}
							filteredNodes = append(filteredNodes, gwNode)
						}
					}
				}
			}
		}

	}
	return
}

func FilterNetworksByRole(allnetworks []models.Network, user models.User) []models.Network {
	platformRole, err := logic.GetRole(user.PlatformRoleID)
	if err != nil {
		return []models.Network{}
	}
	if !platformRole.FullAccess {
		allNetworkRoles := make(map[models.NetworkID]struct{})

		if len(user.UserGroups) > 0 {
			for userGID := range user.UserGroups {
				userG, err := GetUserGroup(userGID)
				if err == nil {
					if len(userG.NetworkRoles) > 0 {
						for netID := range userG.NetworkRoles {
							if netID == models.AllNetworks {
								return allnetworks
							}
							allNetworkRoles[netID] = struct{}{}

						}
					}
				}
			}
		}
		filteredNetworks := []models.Network{}
		for _, networkI := range allnetworks {
			if _, ok := allNetworkRoles[models.NetworkID(networkI.NetID)]; ok {
				filteredNetworks = append(filteredNetworks, networkI)
			}
		}
		allnetworks = filteredNetworks
	}
	return allnetworks
}

func IsGroupsValid(groups map[models.UserGroupID]struct{}) error {

	for groupID := range groups {
		_, err := GetUserGroup(groupID)
		if err != nil {
			return fmt.Errorf("user group `%s` not found", groupID)
		}
	}
	return nil
}

func IsGroupValid(groupID models.UserGroupID) error {

	_, err := GetUserGroup(groupID)
	if err != nil {
		return fmt.Errorf("user group `%s` not found", groupID)
	}

	return nil
}

func IsNetworkRolesValid(networkRoles map[models.NetworkID]map[models.UserRoleID]struct{}) error {
	for netID, netRoles := range networkRoles {

		if netID != models.AllNetworks {
			_, err := logic.GetNetwork(netID.String())
			if err != nil {
				return fmt.Errorf("failed to fetch network %s ", netID)
			}
		}
		for netRoleID := range netRoles {
			_, err := logic.GetRole(netRoleID)
			if err != nil {
				return fmt.Errorf("failed to fetch role %s ", netRoleID)
			}

		}
	}
	return nil
}

// PrepareOauthUserFromInvite - init oauth user before create
func PrepareOauthUserFromInvite(in models.UserInvite) (models.User, error) {
	var newPass, fetchErr = logic.FetchPassValue("")
	if fetchErr != nil {
		return models.User{}, fetchErr
	}
	user := models.User{
		UserName: in.Email,
		Password: newPass,
	}
	user.UserGroups = in.UserGroups
	user.PlatformRoleID = models.UserRoleID(in.PlatformRoleID)
	if user.PlatformRoleID == "" {
		user.PlatformRoleID = models.ServiceUser
	}
	return user, nil
}

func UpdatesUserGwAccessOnGrpUpdates(currNetworkRoles, changeNetworkRoles map[models.NetworkID]map[models.UserRoleID]struct{}) {
	networkChangeMap := make(map[models.NetworkID]map[models.UserRoleID]struct{})
	for netID, networkUserRoles := range currNetworkRoles {
		if _, ok := changeNetworkRoles[netID]; !ok {
			for netRoleID := range networkUserRoles {
				if _, ok := networkChangeMap[netID]; !ok {
					networkChangeMap[netID] = make(map[models.UserRoleID]struct{})
				}
				networkChangeMap[netID][netRoleID] = struct{}{}
			}
		} else {
			for netRoleID := range networkUserRoles {
				if _, ok := changeNetworkRoles[netID][netRoleID]; !ok {
					if _, ok := networkChangeMap[netID]; !ok {
						networkChangeMap[netID] = make(map[models.UserRoleID]struct{})
					}
					networkChangeMap[netID][netRoleID] = struct{}{}
				}
			}
		}
	}
	extclients, err := logic.GetAllExtClients()
	if err != nil {
		slog.Error("failed to fetch extclients", "error", err)
		return
	}
	userMap, err := logic.GetUserMap()
	if err != nil {
		return
	}
	for _, extclient := range extclients {

		if _, ok := networkChangeMap[models.NetworkID(extclient.Network)]; ok {
			if user, ok := userMap[extclient.OwnerID]; ok {
				if user.PlatformRoleID != models.ServiceUser {
					continue
				}
				err = logic.DeleteExtClientAndCleanup(extclient)
				if err != nil {
					slog.Error("failed to delete extclient",
						"id", extclient.ClientID, "owner", user.UserName, "error", err)
				} else {
					if err := mq.PublishDeletedClientPeerUpdate(&extclient); err != nil {
						slog.Error("error setting ext peers: " + err.Error())
					}
				}
			}

		}

	}
	if servercfg.IsDNSMode() {
		logic.SetDNS()
	}

}

func CreateDefaultUserPolicies(netID models.NetworkID) {
	if netID.String() == "" {
		return
	}
	// if !logic.IsAclExists(models.AclID(fmt.Sprintf("%s.%s", netID, models.NetworkAdmin))) {
	// 	defaultUserAcl := models.Acl{
	// 		ID:        models.AclID(fmt.Sprintf("%s.%s", netID, models.NetworkAdmin)),
	// 		Name:      models.NetworkAdmin.String(),
	// 		Default:   true,
	// 		NetworkID: netID,
	// 		RuleType:  models.UserPolicy,
	// 		Src: []models.AclPolicyTag{
	// 			{
	// 				ID:    models.UserRoleAclID,
	// 				Value: fmt.Sprintf("%s-%s", netID, models.NetworkAdmin),
	// 			}},
	// 		Dst: []models.AclPolicyTag{
	// 			{
	// 				ID:    models.DeviceAclID,
	// 				Value: fmt.Sprintf("%s.%s", netID, models.RemoteAccessTagName),
	// 			},
	// 		},
	// 		AllowedDirection: models.TrafficDirectionUni,
	// 		Enabled:          true,
	// 		CreatedBy:        "auto",
	// 		CreatedAt:        time.Now().UTC(),
	// 	}
	// 	logic.InsertAcl(defaultUserAcl)
	// }
	// if !logic.IsAclExists(models.AclID(fmt.Sprintf("%s.%s", netID, models.NetworkUser))) {
	// 	defaultUserAcl := models.Acl{
	// 		ID:        models.AclID(fmt.Sprintf("%s.%s", netID, models.NetworkUser)),
	// 		Name:      models.NetworkUser.String(),
	// 		Default:   true,
	// 		NetworkID: netID,
	// 		RuleType:  models.UserPolicy,
	// 		Src: []models.AclPolicyTag{
	// 			{
	// 				ID:    models.UserRoleAclID,
	// 				Value: fmt.Sprintf("%s-%s", netID, models.NetworkUser),
	// 			}},
	// 		Dst: []models.AclPolicyTag{
	// 			{
	// 				ID:    models.DeviceAclID,
	// 				Value: fmt.Sprintf("%s.%s", netID, models.RemoteAccessTagName),
	// 			}},
	// 		AllowedDirection: models.TrafficDirectionUni,
	// 		Enabled:          true,
	// 		CreatedBy:        "auto",
	// 		CreatedAt:        time.Now().UTC(),
	// 	}
	// 	logic.InsertAcl(defaultUserAcl)
	// }

	if !logic.IsAclExists(models.AclID(fmt.Sprintf("%s.%s-grp", netID, models.NetworkAdmin))) {
		defaultUserAcl := models.Acl{
			ID:        models.AclID(fmt.Sprintf("%s.%s-grp", netID, models.NetworkAdmin)),
			Name:      fmt.Sprintf("%s-grp", models.NetworkAdmin),
			Default:   true,
			NetworkID: netID,
			RuleType:  models.UserPolicy,
			Src: []models.AclPolicyTag{
				{
					ID:    models.UserGroupAclID,
					Value: fmt.Sprintf("%s-%s-grp", netID, models.NetworkAdmin),
				}},
			Dst: []models.AclPolicyTag{
				{
					ID:    models.DeviceAclID,
					Value: fmt.Sprintf("%s.%s", netID, models.RemoteAccessTagName),
				}},
			AllowedDirection: models.TrafficDirectionUni,
			Enabled:          true,
			CreatedBy:        "auto",
			CreatedAt:        time.Now().UTC(),
		}
		logic.InsertAcl(defaultUserAcl)
	}

	if !logic.IsAclExists(models.AclID(fmt.Sprintf("%s.%s-grp", netID, models.NetworkUser))) {
		defaultUserAcl := models.Acl{
			ID:        models.AclID(fmt.Sprintf("%s.%s-grp", netID, models.NetworkUser)),
			Name:      fmt.Sprintf("%s-grp", models.NetworkUser),
			Default:   true,
			NetworkID: netID,
			RuleType:  models.UserPolicy,
			Src: []models.AclPolicyTag{
				{
					ID:    models.UserGroupAclID,
					Value: fmt.Sprintf("%s-%s-grp", netID, models.NetworkUser),
				}},
			Dst: []models.AclPolicyTag{
				{
					ID:    models.DeviceAclID,
					Value: fmt.Sprintf("%s.%s", netID, models.RemoteAccessTagName),
				}},
			AllowedDirection: models.TrafficDirectionUni,
			Enabled:          true,
			CreatedBy:        "auto",
			CreatedAt:        time.Now().UTC(),
		}
		logic.InsertAcl(defaultUserAcl)
	}
}
