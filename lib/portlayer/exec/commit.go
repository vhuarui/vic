// Copyright 2016-2017 VMware, Inc. All Rights Reserved.
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

package exec

import (
	"context"
	"fmt"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
	"github.com/vmware/vic/lib/portlayer/event/events"
	"github.com/vmware/vic/pkg/trace"
	"github.com/vmware/vic/pkg/vsphere/session"
	"github.com/vmware/vic/pkg/vsphere/tasks"
	"github.com/vmware/vic/pkg/vsphere/vm"

	log "github.com/Sirupsen/logrus"
)

// Commit executes the requires steps on the handle
func Commit(ctx context.Context, sess *session.Session, h *Handle, waitTime *int32) error {
	defer trace.End(trace.Begin(h.ExecConfig.ID))

	c := Containers.Container(h.ExecConfig.ID)
	creation := h.vm == nil
	if creation {
		if h.Spec == nil {
			return fmt.Errorf("a spec must be provided for create operations")
		}

		if sess == nil {
			// session must not be nil
			return fmt.Errorf("no session provided for create operations")
		}

		// the only permissible operation is to create a VM
		if h.Spec == nil {
			return fmt.Errorf("only create operations can be committed without an existing VM")
		}

		if c != nil {
			return fmt.Errorf("a container already exists in the cache with this ID")
		}

		var res *types.TaskInfo
		var err error
		if sess.IsVC() && Config.VirtualApp.ResourcePool != nil {
			// Create the vm
			res, err = tasks.WaitForResult(ctx, func(ctx context.Context) (tasks.Task, error) {
				return Config.VirtualApp.CreateChildVM(ctx, *h.Spec.Spec(), nil)
			})
		} else {
			// Find the Virtual Machine folder that we use
			var folders *object.DatacenterFolders
			folders, err = sess.Datacenter.Folders(ctx)
			if err != nil {
				log.Errorf("Could not get folders")
				return err
			}
			parent := folders.VmFolder

			// Create the vm
			res, err = tasks.WaitForResult(ctx, func(ctx context.Context) (tasks.Task, error) {
				return parent.CreateVM(ctx, *h.Spec.Spec(), Config.ResourcePool, nil)
			})
		}

		if err != nil {
			log.Errorf("An error occurred while waiting for a creation operation to complete. Spec was %+v", *h.Spec.Spec())
			return err
		}

		h.vm = vm.NewVirtualMachine(ctx, sess, res.Result.(types.ManagedObjectReference))
		c = newContainer(&h.containerBase)
		Containers.Put(c)
		// inform of creation irrespective of remaining operations
		publishContainerEvent(c.ExecConfig.ID, time.Now().UTC(), events.ContainerCreated)

		// clear the spec as we've acted on it - this prevents a reconfigure from occurring in follow-on
		// processing
		h.Spec = nil
	}

	// if we're stopping the VM, do so before the reconfigure to preserve the extraconfig
	if h.TargetState() == StateStopped {
		if h.Runtime == nil {
			log.Warnf("Commit called with incomplete runtime state for %s", h.ExecConfig.ID)
		}

		if h.Runtime != nil && h.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOff {
			log.Infof("Dropping duplicate power off operation for %s", h.ExecConfig.ID)
		} else {
			// stop the container
			if err := c.stop(ctx, waitTime); err != nil {
				return err
			}

			// inform of creation irrespective of remaining operations
			publishContainerEvent(h.ExecConfig.ID, time.Now().UTC(), events.ContainerStopped)

			// we must refresh now to get the new ChangeVersion - this is used to gate on powerstate in the reconfigure
			// because we cannot set the ExtraConfig if the VM is powered on. There is still a race here unfortunately because
			// tasks don't appear to contain the new ChangeVersion
			// we don't use refresh because we want to keep the extraconfig state
			base, err := h.updates(ctx)
			if err != nil {
				// TODO: can we recover here, or at least set useful state for inspection?
				return err
			}
			h.Runtime = base.Runtime
			h.Config = base.Config
		}
	}

	// reconfigure operation
	if h.Spec != nil {
		if h.Runtime == nil {
			log.Errorf("Refusing to perform reconfigure operation with incomplete runtime state for %s", h.ExecConfig.ID)
		} else {
			// ensure that our logic based on Runtime state remains valid

			// NOTE: this inline refresh can be removed when switching away from guestinfo where we have non-persistence issues
			// when updating ExtraConfig via the API with a powered on VM - we therefore have to be absolutely certain about the
			// power state to decide if we can continue without nilifying extraconfig
			s := h.Spec.Spec()

			// poor man's test and set
			s.ChangeVersion = h.Config.ChangeVersion
			log.Debugf("ChangeVersion is %s", s.ChangeVersion)

			// nilify ExtraConfig if container configuration is migrated
			// in this case, VCH and container are in different version. Migrated configuration cannot be written back to old container, to avoid data loss in old version's container
			if h.Migrated {
				log.Debugf("Nilifying ExtraConfig as configuration of container %s is migrated", h.ExecConfig.ID)
				s.ExtraConfig = nil
			}

			_, err := h.vm.WaitForResult(ctx, func(ctx context.Context) (tasks.Task, error) {
				return h.vm.Reconfigure(ctx, *s)
			})
			if err != nil {
				log.Errorf("Reconfigure failed with %#+v", err)

				// Check whether we get ConcurrentAccess and wrap it if needed
				if f, ok := err.(types.HasFault); ok {
					switch f.Fault().(type) {
					case *types.ConcurrentAccess:
						log.Errorf("We have ConcurrentAccess for version %s", s.ChangeVersion)

						return ConcurrentAccessError{err}
					}
				}
				return err
			}

			// trigger a configuration reload in the container if needed
			if h.reload && h.Runtime != nil && h.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn {
				err = h.startGuestProgram(ctx, "reload", "")
				if err != nil {
					// NOTE: not sure how to handle this error - the change is already applied, it's just not picked up in the container
				}
			}
		}
	}

	// best effort update of container cache using committed state - this will not reflect the power on below, however
	// this is primarily for updating ExtraConfig state.
	if !creation {
		defer c.RefreshFromHandle(ctx, h)
	}

	if h.TargetState() == StateRunning {
		if h.Runtime != nil && h.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn {
			log.Infof("Dropping duplicate power on operation for %s", h.ExecConfig.ID)
			return nil
		}

		if h.Runtime == nil && !creation {
			log.Warnf("Commit called with incomplete runtime state for %s", h.ExecConfig.ID)
		}

		// start the container
		if err := c.start(ctx); err != nil {
			return err
		}

		// inform of creation irrespective of remaining operations
		publishContainerEvent(h.ExecConfig.ID, time.Now().UTC(), events.ContainerStarted)
	}

	return nil
}
