// Copyright 2016 VMware, Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package simulator

import (
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

type SessionManager struct {
	mo.SessionManager
}

func NewSessionManager(ref types.ManagedObjectReference) object.Reference {
	s := &SessionManager{}
	s.Self = ref
	return s
}

func (s *SessionManager) Login(login *types.Login) soap.HasFault {
	body := &methods.LoginBody{}

	if login.UserName == "" || login.Password == "" {
		body.Fault_ = Fault("Login failure", &types.InvalidLogin{})
	} else {
		body.Res = &types.LoginResponse{
			Returnval: types.UserSession{
				UserName:  login.UserName,
				FullName:  login.UserName,
				LoginTime: time.Now(),
			},
		}
	}

	return body
}