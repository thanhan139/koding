package oskite

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/ioutil"
	"koding/db/mongodb"
	"koding/db/mongodb/modelhelper"
	"koding/kodingkite"
	"koding/oskite/ldapserver"
	"koding/tools/config"
	"koding/tools/dnode"
	"koding/tools/kite"
	"koding/tools/lifecycle"
	"koding/tools/logger"
	"koding/tools/utils"
	"koding/virt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	kitelib "github.com/koding/kite"

	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
)

const (
	OSKITE_NAME    = "oskite"
	OSKITE_VERSION = "0.1.5"
)

var (
	log         = logger.New(OSKITE_NAME)
	mongodbConn *mongodb.MongoDB
	conf        *config.Config

	templateDir      = "files/templates" // should be in the same dir as the binary
	firstContainerIP net.IP
	containerSubnet  *net.IPNet
	shuttingDown     = false
	requestWaitGroup sync.WaitGroup

	prepareQueue      = make(chan *QueueJob, 1000)
	currentQueueCount AtomicInt32
	vmTimeout         time.Duration
)

type Oskite struct {
	Name     string
	Version  string
	Region   string
	LogLevel logger.Level

	ActiveVMsLimit int
	ActiveVMs      int

	ServiceUniquename string
	VmTimeout         time.Duration
	TemplateDir       string
	DisableGuest      bool

	// PrepareQueueLimit defines the number of concurrent VM preparations,
	// should be CPU + 1
	PrepareQueueLimit int
}

// QueueJob is used to append jobs to the prepareQueue.
type QueueJob struct {
	f   func() (string, error)
	msg string
}

func New(c *config.Config) *Oskite {
	conf = c
	mongodbConn = mongodb.NewMongoDB(c.Mongo)
	modelhelper.Initialize(c.Mongo)

	return &Oskite{
		Name:    OSKITE_NAME,
		Version: OSKITE_VERSION,
	}
}

func (o *Oskite) Run() {

	log.SetLevel(o.LogLevel)

	log.Info("Using default VM timeout: %v", o.VmTimeout)

	// TODO: get rid of this after solving info problem
	vmTimeout = o.VmTimeout

	if o.Region == "" {
		panic("region is not set for Oskite")
	}

	if o.ActiveVMsLimit == 0 {
		panic("active VMS limit is not defined.")
	}

	if o.PrepareQueueLimit == 0 {
		panic("prepare queue is not set")
	}

	if o.TemplateDir != "" {
		templateDir = o.TemplateDir
	}

	// set seed for even randomness, needed for randomMinutes() function.
	rand.Seed(time.Now().UnixNano())

	o.initializeSettings()

	// startPrepareWorkers starts multiple workers (based on prepareQueueLimit)
	// that accepts vmPrepare/vmStart functions.
	for i := 0; i < o.PrepareQueueLimit; i++ {
		go prepareWorker(i)
	}

	k := o.prepareOsKite()

	o.ServiceUniquename = k.ServiceUniqueName

	o.runNewKite()
	o.handleCurrentVMS()   // handle leftover VMs
	o.startPinnedVMS()     // start pinned always-on VMs
	o.setupSignalHandler() // handle SIGUSR1 and other signals.

	// register current client-side methods
	o.registerMethod(k, "vm.start", false, vmStartOld)
	o.registerMethod(k, "vm.prepareAndStart", false, vmPrepareAndStart)
	o.registerMethod(k, "vm.stopAndUnprepare", false, vmStopAndUnprepare)
	o.registerMethod(k, "vm.shutdown", false, vmShutdownOld)
	o.registerMethod(k, "vm.unprepare", false, vmUnprepareOld)
	o.registerMethod(k, "vm.prepare", false, vmPrepareOld)
	o.registerMethod(k, "vm.stop", false, vmStopOld)
	o.registerMethod(k, "vm.reinitialize", false, vmReinitializeOld)
	o.registerMethod(k, "vm.info", false, vmInfoOld)
	o.registerMethod(k, "vm.resizeDisk", false, vmResizeDiskOld)
	o.registerMethod(k, "vm.createSnapshot", false, vmCreateSnapshotOld)
	o.registerMethod(k, "spawn", true, spawnFuncOld)
	o.registerMethod(k, "exec", true, execFuncOld)

	o.registerMethod(k, "oskite.Info", true, o.oskiteInfo)
	o.registerMethod(k, "oskite.All", true, oskiteAll)

	syscall.Umask(0) // don't know why richard calls this
	o.registerMethod(k, "fs.readDirectory", false, fsReadDirectoryOld)
	o.registerMethod(k, "fs.glob", false, fsGlobOld)
	o.registerMethod(k, "fs.readFile", false, fsReadFileOld)
	o.registerMethod(k, "fs.writeFile", false, fsWriteFileOld)
	o.registerMethod(k, "fs.uniquePath", false, fsUniquePathOld)
	o.registerMethod(k, "fs.getInfo", false, fsGetInfoOld)
	o.registerMethod(k, "fs.setPermissions", false, fsSetPermissionsOld)
	o.registerMethod(k, "fs.remove", false, fsRemoveOld)
	o.registerMethod(k, "fs.rename", false, fsRenameOld)
	o.registerMethod(k, "fs.createDirectory", false, fsCreateDirectoryOld)
	o.registerMethod(k, "fs.move", false, fsMoveOld)
	o.registerMethod(k, "fs.copy", false, fsCopyOld)

	o.registerMethod(k, "app.install", false, appInstallOld)
	o.registerMethod(k, "app.download", false, appDownloadOld)
	o.registerMethod(k, "app.publish", false, appPublishOld)
	o.registerMethod(k, "app.skeleton", false, appSkeletonOld)

	// this method is special cased in oskite.go to allow foreign access
	o.registerMethod(k, "webterm.connect", false, webtermConnectOld)
	o.registerMethod(k, "webterm.getSessions", false, webtermGetSessionsOld)

	o.registerMethod(k, "s3.store", true, s3StoreOld)
	o.registerMethod(k, "s3.delete", true, s3DeleteOld)

	go o.redisBalancer(k.ServiceUniqueName)

	log.Info("Oskite started. Go!")
	k.Run()

}

func (o *Oskite) runNewKite() {
	log.Info("Run newkite.")
	k := kodingkite.New(
		conf,
		kitelib.Options{
			Kitename: OSKITE_NAME,
			Version:  "0.0.1",
			Port:     "5000",
			Region:   o.Region,
		},
	)

	o.vosMethod(k, "vm.start", vmStartNew)
	o.vosMethod(k, "vm.prepareAndStart", vmPrepareAndStartNew)
	o.vosMethod(k, "vm.stopAndUnprepare", vmStopAndUnprepareNew)
	o.vosMethod(k, "vm.shutdown", vmShutdownNew)
	o.vosMethod(k, "vm.prepare", vmPrepareNew)
	o.vosMethod(k, "vm.unprepare", vmUnprepareNew)
	o.vosMethod(k, "vm.stop", vmStopNew)
	o.vosMethod(k, "vm.reinitialize", vmReinitializeNew)
	o.vosMethod(k, "vm.info", vmInfoNew)
	o.vosMethod(k, "vm.resizeDisk", vmResizeDiskNew)
	o.vosMethod(k, "vm.createSnapshot", vmCreateSnapshotNew)
	o.vosMethod(k, "spawn", spawnFuncNew)
	o.vosMethod(k, "exec", execFuncNew)

	o.vosMethod(k, "oskite.Info", o.oskiteInfoNew)
	o.vosMethod(k, "oskite.All", oskiteAllNew)

	o.vosMethod(k, "fs.readDirectory", fsReadDirectoryNew)
	o.vosMethod(k, "fs.glob", fsGlobNew)
	o.vosMethod(k, "fs.readFile", fsReadFileNew)
	o.vosMethod(k, "fs.writeFile", fsWriteFileNew)
	o.vosMethod(k, "fs.uniquePath", fsUniquePathNew)
	o.vosMethod(k, "fs.getInfo", fsGetInfoNew)
	o.vosMethod(k, "fs.setPermissions", fsSetPermissionsNew)
	o.vosMethod(k, "fs.remove", fsRemoveNew)
	o.vosMethod(k, "fs.rename", fsRenameNew)
	o.vosMethod(k, "fs.createDirectory", fsCreateDirectoryNew)
	o.vosMethod(k, "fs.move", fsMoveNew)
	o.vosMethod(k, "fs.copy", fsCopyNew)

	o.vosMethod(k, "app.install", appInstallNew)
	o.vosMethod(k, "app.download", appDownloadNew)
	o.vosMethod(k, "app.publish", appPublishNew)
	o.vosMethod(k, "app.skeleton", appSkeletonNew)

	o.vosMethod(k, "webterm.connect", webtermConnectNew)
	o.vosMethod(k, "webterm.getSessions", webtermGetSessionsNew)

	o.vosMethod(k, "s3.store", s3StoreNew)
	o.vosMethod(k, "s3.delete", s3DeleteNew)

	k.DisableConcurrency() // needed for webterm.connect
	k.Start()

	// TODO: remove this later, this is needed in order to reinitiliaze the logger package
	log.SetLevel(o.LogLevel)
}

func (o *Oskite) initializeSettings() {
	lifecycle.Startup("kite.os", true)

	var err error
	if firstContainerIP, containerSubnet, err = net.ParseCIDR(conf.ContainerSubnet); err != nil {
		log.LogError(err, 0)
		return
	}

	virt.VMPool = conf.VmPool
	if err := virt.LoadTemplates(templateDir); err != nil {
		log.LogError(err, 0)
		return
	}

	go ldapserver.Listen(conf.Mongo)
	go LimiterLoop()
}

func (o *Oskite) prepareOsKite() *kite.Kite {
	log.Info("Kite.go preperation started")
	kiteName := "os"
	if o.Region != "" {
		kiteName += "-" + o.Region
	}

	k := kite.New(kiteName, conf, true)

	// Default is "broker", we are going to use another one. In our case its "brokerKite"
	k.PublishExchange = conf.BrokerKite.Name

	if o.LogLevel == logger.DEBUG {
		kite.EnableDebug()
	}

	k.LoadBalancer = func(correlationName string, username string, deadService string) string {
		blog := func(v interface{}) {
			log.Info("oskite loadbalancer for [correlationName: '%s' user: '%s' deadService: '%s'] results in --> %v.", correlationName, username, deadService, v)
		}

		resultOskite := o.ServiceUniquename
		lowestOskite := lowestOskiteLoad()
		if lowestOskite != "" {
			if deadService == lowestOskite {
				resultOskite = o.ServiceUniquename
			} else {
				resultOskite = lowestOskite
			}
		}

		var vm *virt.VM
		if bson.IsObjectIdHex(correlationName) {
			mongodbConn.Run("jVMs", func(c *mgo.Collection) error {
				return c.FindId(bson.ObjectIdHex(correlationName)).One(&vm)
			})
		}

		if vm == nil {
			if err := mongodbConn.Run("jVMs", func(c *mgo.Collection) error {
				return c.Find(bson.M{"hostnameAlias": correlationName}).One(&vm)
			}); err != nil {
				blog(fmt.Sprintf("no hostnameAlias found, returning %s", resultOskite))
				return resultOskite // no vm was found, return this oskite
			}
		}

		if vm.PinnedToHost != "" {
			blog(fmt.Sprintf("returning pinnedHost '%s'", vm.PinnedToHost))
			return vm.PinnedToHost
		}

		if vm.HostKite == "" {
			blog(fmt.Sprintf("hostkite is empty returning '%s'", resultOskite))
			return resultOskite
		}

		// maintenance and banned will be handled again in valideVM() function,
		// which will return a permission error.
		if vm.HostKite == "(maintenance)" || vm.HostKite == "(banned)" {
			blog(fmt.Sprintf("hostkite is %s returning '%s'", vm.HostKite, resultOskite))
			return resultOskite
		}

		// Set hostkite to nil if we detect a dead service. On the next call,
		// Oskite will point to an health service in validateVM function()
		// because it will detect that the hostkite is nil and change it to the
		// healthy service given by the client, which is the returned
		// k.ServiceUniqueName.
		if vm.HostKite == deadService {
			blog(fmt.Sprintf("dead service detected %s returning '%s'", vm.HostKite, o.ServiceUniquename))
			if err := mongodbConn.Run("jVMs", func(c *mgo.Collection) error {
				return c.Update(bson.M{"_id": vm.Id}, bson.M{"$set": bson.M{"hostKite": nil}})
			}); err != nil {
				log.LogError(err, 0, vm.Id.Hex())
			}

			return resultOskite
		}

		blog(fmt.Sprintf("returning existing hostkite '%s'", vm.HostKite))
		return vm.HostKite
	}

	return k
}

// handleCurrentVMS removes and unprepare any vm in the lxc dir that doesn't
// have any associated document which in mongodbConn.
func (o *Oskite) handleCurrentVMS() {
	dirs, err := ioutil.ReadDir("/var/lib/lxc")
	if err != nil {
		log.LogError(err, 0)
		return
	}

	for _, dir := range dirs {
		if strings.HasPrefix(dir.Name(), "vm-") {
			vmId := bson.ObjectIdHex(dir.Name()[3:])
			var vm virt.VM
			query := func(c *mgo.Collection) error {
				return c.FindId(vmId).One(&vm)
			}

			if err := mongodbConn.Run("jVMs", query); err != nil || vm.HostKite != o.ServiceUniquename {
				log.Info("cleaning up leftover VM: '%s', vm.Hoskite: '%s', k.ServiceUniqueName: '%s', error '%v'",
					vmId, vm.HostKite, o.ServiceUniquename, err)

				if err := virt.UnprepareVM(vmId); err != nil {
					log.Error("%v", err)
				}
				continue
			}

			vm.ApplyDefaults()
			info := newInfo(&vm)
			infos[vm.Id] = info
			info.startTimeout()
		}
	}

	log.Info("VMs in /var/lib/lxc are finished.")
}

func (o *Oskite) startPinnedVMS() {
	log.Info("Starting pinned hosts, if any...")
	mongodbConn.Run("jVMs", func(c *mgo.Collection) error {
		iter := c.Find(bson.M{"pinnedToHost": o.ServiceUniquename, "alwaysOn": true}).Iter()
		for {
			var vm virt.VM
			if !iter.Next(&vm) {
				break
			}
			if err := o.startVM(&vm, nil); err != nil {
				log.LogError(err, 0)
			}
		}

		if err := iter.Close(); err != nil {
			panic(err)
		}

		return nil
	})
}

func (o *Oskite) setupSignalHandler() {
	log.Info("Setting up signal handler")
	sigtermChannel := make(chan os.Signal)
	signal.Notify(sigtermChannel, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)
	go func() {
		sig := <-sigtermChannel
		log.Info("Shutdown initiated.")
		shuttingDown = true
		requestWaitGroup.Wait()
		if sig == syscall.SIGUSR1 {
			for _, info := range infos {
				log.Info("Unpreparing " + info.vm.String() + "...")
				prepareQueue <- &QueueJob{
					msg: "vm unprepare because of shutdown oskite " + info.vm.HostnameAlias,
					f: func() (string, error) {
						// mutex is needed because it's handled in the queue
						info.mutex.Lock()
						defer info.mutex.Unlock()

						info.unprepareVM()
						return fmt.Sprintf("shutting down %s", info.vm.Id.Hex()), nil
					},
				}
			}

			query := func(c *mgo.Collection) error {
				_, err := c.UpdateAll(
					bson.M{"hostKite": o.ServiceUniquename},
					bson.M{"$set": bson.M{"hostKite": nil}},
				) // ensure that really all are set to nil
				return err
			}

			err := mongodbConn.Run("jVMs", query)
			if err != nil {
				log.LogError(err, 0)
			}
		}

		log.Fatal()
	}()
}

// registerMethod is wrapper around our final methods. It's basically creates
// a "vos" struct and pass it to the our method. The VOS has "vm", "user" and
// "permissions" document embedded, with this info our final method has all
// the necessary needed bits.
func (o *Oskite) registerMethod(k *kite.Kite, method string, concurrent bool, callback func(*dnode.Partial, *kite.Channel, *virt.VOS) (interface{}, error)) {

	wrapperMethod := func(args *dnode.Partial, channel *kite.Channel) (methodReturnValue interface{}, methodError error) {
		// set to true when a SIGNAL is received
		if shuttingDown {
			return nil, errors.New("Kite is shutting down.")
		}

		log.Info("[%s] is called. user [%s] [%s]", method, channel.Username, channel.CorrelationName)

		// Needed when we oskite get closed via a SIGNAL. It waits until all methods are done.
		requestWaitGroup.Add(1)
		defer requestWaitGroup.Done()

		user, err := o.getUser(channel.Username)
		if err != nil {
			return nil, err
		}

		vm, err := o.getVM(channel.CorrelationName)
		if err != nil {
			return nil, err
		}

		defer func() {
			if err := recover(); err != nil {
				log.LogError(err, 1, channel.Username, channel.CorrelationName, vm.String())
				time.Sleep(time.Second) // penalty for avoiding that the client rapidly sends the request again on error
				methodError = &kite.InternalKiteError{}
			}
		}()

		// this method is special cased in oskite.go to allow foreign access.
		// that's why do not check for permisisons and return early.
		if method == "webterm.connect" {
			return callback(args, channel, &virt.VOS{VM: vm, User: user})
		}

		// protect each callback with their own associated mutex
		info := getInfo(vm)
		info.mutex.Lock()
		defer info.mutex.Unlock()
		info.stopTimeout(channel)

		err = o.validateVM(vm)
		if err != nil {
			return nil, err
		}

		// vos has now "vm", "user" and "permissions" document.
		vos, err := vm.OS(user)
		if err != nil {
			return nil, err // returns an error if the permisisons are not set for the user
		}

		// now call our final method. run forrest run ....
		return callback(args, channel, vos)
	}

	k.Handle(method, concurrent, wrapperMethod)
}

func (o *Oskite) getUser(username string) (*virt.User, error) {
	// Do not create guest vms if its turned of
	if o.DisableGuest && strings.HasPrefix(username, "guest-") {
		return nil, errors.New("vm creation for guests are disabled.")
	}

	var user *virt.User
	if err := mongodbConn.Run("jUsers", func(c *mgo.Collection) error {
		return c.Find(bson.M{"username": username}).One(&user)
	}); err != nil {
		if err != mgo.ErrNotFound {
			return nil, fmt.Errorf("username lookup error: %v", err)
		}

		if !strings.HasPrefix(username, "guest-") {
			log.Warning("User not found: %v", username)
		}

		time.Sleep(time.Second) // to avoid rapid cycle channel loop
		return nil, &kite.WrongChannelError{}
	}

	if user.Uid < virt.UserIdOffset {
		return nil, errors.New("User with too low uid.")
	}

	return user, nil
}

// getVM returns a new virt.VM struct based on on the given correlationName.
// Here correlationName can be either the hostnameAlias or the given VM
// documents ID.
func (o *Oskite) getVM(correlationName string) (*virt.VM, error) {
	var vm *virt.VM
	query := bson.M{"hostnameAlias": correlationName}
	if bson.IsObjectIdHex(correlationName) {
		query = bson.M{"_id": bson.ObjectIdHex(correlationName)}
	}

	if err := mongodbConn.Run("jVMs", func(c *mgo.Collection) error {
		return c.Find(query).One(&vm)
	}); err != nil {
		return nil, &VMNotFoundError{Name: correlationName}
	}

	vm.ApplyDefaults()
	return vm, nil
}

func (o *Oskite) validateVM(vm *virt.VM) error {
	if vm.Region != o.Region {
		time.Sleep(time.Second) // to avoid rapid cycle channel loop
		return &kite.WrongChannelError{}
	}

	if vm.HostKite == "(maintenance)" {
		return &UnderMaintenanceError{}
	}

	if vm.HostKite == "(banned)" {
		log.Warning("vm '%s' is banned", vm.HostnameAlias)
		return &AccessDeniedError{}
	}

	if vm.IP == nil {
		ipInt := NextCounterValue("vm_ip", int(binary.BigEndian.Uint32(firstContainerIP.To4())))
		ip := net.IPv4(byte(ipInt>>24), byte(ipInt>>16), byte(ipInt>>8), byte(ipInt))

		updateErr := mongodbConn.Run("jVMs", func(c *mgo.Collection) error {
			return c.Update(bson.M{"_id": vm.Id, "ip": nil}, bson.M{"$set": bson.M{"ip": ip}})
		})

		if updateErr != nil {
			var logVM *virt.VM
			err := mongodbConn.One("jVMs", vm.Id.Hex(), &logVM)
			if err != nil {
				errLog := fmt.Sprintf("Vm %s does not exist for updating IP. This is a race condition", vm.Id.Hex())
				log.LogError(errLog, 0)
			} else {
				errLog := fmt.Sprintf("Vm %s does exist for updating IP but it tries to replace it. This is a race condition", vm.Id.Hex())
				log.LogError(errLog, 0, logVM)
			}

			return updateErr
		}

		vm.IP = ip
	}

	if !containerSubnet.Contains(vm.IP) {
		return errors.New("VM with IP that is not in the container subnet: " + vm.IP.String())
	}

	if vm.LdapPassword == "" {
		ldapPassword := utils.RandomString()
		if err := mongodbConn.Run("jVMs", func(c *mgo.Collection) error {
			return c.Update(bson.M{"_id": vm.Id}, bson.M{"$set": bson.M{"ldapPassword": ldapPassword}})
		}); err != nil {
			return err
		}
		vm.LdapPassword = ldapPassword
	}

	if vm.HostKite != o.ServiceUniquename {
		err := mongodbConn.Run("jVMs", func(c *mgo.Collection) error {
			return c.Update(bson.M{"_id": vm.Id, "hostKite": nil}, bson.M{"$set": bson.M{"hostKite": o.ServiceUniquename}})
		})
		if err != nil {
			time.Sleep(time.Second) // to avoid rapid cycle channel loop
			return &kite.WrongChannelError{}
		}

		vm.HostKite = o.ServiceUniquename
	}

	return nil
}

func (o *Oskite) startVM(vm *virt.VM, channel *kite.Channel) error {
	info := getInfo(vm)
	info.mutex.Lock()
	defer info.mutex.Unlock()
	info.stopTimeout(channel)

	err := o.validateVM(vm)
	if err != nil {
		return err
	}

	return startAndPrepareVM(vm)
}

// prepareWorker listens from prepareQueue channel and runs the functions it receives
func prepareWorker(id int) {
	for job := range prepareQueue {
		currentQueueCount.Add(1)

		log.Info(fmt.Sprintf("Queue %d: processing new job [%s]", id, time.Now().Format(time.StampMilli)))

		done := make(chan struct{}, 1)
		go func() {
			startTime := time.Now()
			res, err := job.f() // execute our function
			if err != nil {
				log.Error(fmt.Sprintf("Queue %d: error %s", id, err))
			} else {
				log.Info(fmt.Sprintf("Queue %d: elapsed time %s res: %s", id, time.Since(startTime), res))
			}

			done <- struct{}{}
		}()

		select {
		case <-done:
			log.Info(fmt.Sprintf("Queue %d: done for job: %s", id, job.msg))
		case <-time.After(time.Second * 60):
			log.Info(fmt.Sprintf("Queue %d: timed out after 60 seconds for job: %s", id, job.msg))
		}

		currentQueueCount.Add(-1)
	}
}

type Counter struct {
	Name  string `bson:"_id"`
	Value int    `bson:"seq"`
}

func NextCounterValue(counterName string, initialValue int) int {
	var counter Counter

	if err := mongodbConn.Run("counters", func(c *mgo.Collection) error {
		_, err := c.FindId(counterName).Apply(mgo.Change{Update: bson.M{"$inc": bson.M{"seq": 1}}}, &counter)
		return err
	}); err != nil {
		if err == mgo.ErrNotFound {
			mongodbConn.Run("counters", func(c *mgo.Collection) error {
				c.Insert(Counter{Name: counterName, Value: initialValue})
				return nil // ignore error and try to do atomic update again
			})

			if err := mongodbConn.Run("counters", func(c *mgo.Collection) error {
				_, err := c.FindId(counterName).Apply(mgo.Change{Update: bson.M{"$inc": bson.M{"seq": 1}}}, &counter)
				return err
			}); err != nil {
				panic(err)
			}
			return counter.Value
		}
		panic(err)
	}

	return counter.Value

}
