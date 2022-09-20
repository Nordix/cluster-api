# Machine Timeouts

## Timeouts

Sometimes, to provide a better and fine-graded control over machine management operations, for example, during scaling of nodes, Cluster API provides a set of configurable timeouts for the users. Normally, all available timeouts have their default values set and in case if user wants to configure it, the value provided will take the precedence over the former.
All machine timeouts available are given below:

- `.spec.nodeDrainTimeout` defines a timeout to wait for a Node to be drained by the machine controller before forcibly removing the machine. 
  - Default value is set to 0 seconds.
- `.spec.NodeVolumeDetachTimeout` defines a timeout to wait for all volumes to be detached by the machine controller before forcible removing the machine.
  - Default value is set to 0 seconds.
- `.spec.nodeDeletionTimeout` defines a timeout to wait for deletion of the Node belonging to a Machine, managed by the machine controller before it retries to delete the machine until the timeout is over.
  - Default value is set to 10 seconds.

## Example use-cases

Once timeout is set by the user, machine controller will perform actions within the respective user-provided timeouts. 
Let's see the example use cases for the earlier mentioned timeouts:

1. `.spec.nodeDrainTimeout`:
  - if set to i.e 60 seconds: enables the user to skip the node draining within specified timeout period rather than waiting for it indefinitely. Once timout is over, Machine controller will move on to next check.
1. `.spec.NodeVolumeDetachTimeout`:
  - if set to i.e 60 seconds: enables the user to skip the wait for all volumes to be detached within specified timeout period rather than waiting for it indefinitely. Once timout is over, Machine controller will move on to machine deletion.
1. `.spec.nodeDeletionTimeout`:
  - if set to i.e 60 seconds: makes sure that machine controller will retry to delete the node until the specified time period which happens to be 60 seconds again. Once timout is over, Machine controller will move on without deleting the node.