package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/thogtq/grpc-blog-service/m/v1/blogpb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type server struct{}
type blogItem struct {
	ID       primitive.ObjectID `bson:"_id,omitempty"`
	AuthorID string             `bson:"author_id"`
	Content  string             `bson:"content"`
	Title    string             `bson:"title"`
}

var collection *mongo.Collection

const secureConnection = false
const serverPort = "50051"
const serverAddress = "0.0.0.0:"

func main() {
	//logging line of code causes server crashing
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	//Mongodb connection setup
	client, err := mongo.NewClient(options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		log.Fatalf("error when creating mongodb client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = client.Connect(ctx)
	if err != nil {
		log.Fatalf("can not connect to mongodb server")
	}
	collection = client.Database("mydb").Collection("blog")
	fmt.Println("Connected to MongoDB")

	listen, listenErr := net.Listen("tcp", serverAddress+serverPort)
	if listenErr != nil {
		log.Fatalf("error while listen tcp at %v%v", serverAddress, serverPort)
		return
	}

	//gRPC server setup
	serverOpts := []grpc.ServerOption{}
	if secureConnection {
		//establish TSL connection
	}
	serverControl := grpc.NewServer(serverOpts...)
	blogpb.RegisterBlogServiceServer(serverControl, &server{})
	fmt.Println("Blog Service started")
	//The *Server.Serve() function will block the program so we run it in a goroutine
	go func() {
		fmt.Println("Server started")
		serveErr := serverControl.Serve(listen)
		//Blocked if successfully serve
		if serveErr != nil {
			log.Fatalf("fail to serve server : %v", serveErr)
		}
	}()

	//Setup shutdown hook for server when Ctrl + C
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt)
	//Block until shutdown signal is received
	<-shutdownCh
	//Prepare and shut down server
	fmt.Printf("\nStopping the server...\n")
	serverControl.Stop()
	fmt.Println("Closing the listener...")
	listen.Close()
	fmt.Println("Disconnecting from mongodb...")
	client.Disconnect(ctx)
	fmt.Println("Server shutdown reached")
}
func (*server) CreateBlog(ctx context.Context, req *blogpb.CreateBlogRequest) (*blogpb.CreateBlogResponse, error) {
	blog := req.GetBlog()
	insertData := blogItem{
		AuthorID: blog.GetAuthorId(),
		Title:    blog.GetTitle(),
		Content:  blog.GetContent(),
	}
	resp, insertErr := collection.InsertOne(context.Background(), insertData)
	if insertErr != nil {
		return nil, status.Errorf(
			codes.Internal,
			fmt.Sprintf("Interal error: %v", insertErr),
		)
	}
	blogID, ok := resp.InsertedID.(primitive.ObjectID)
	if !ok {
		return nil, status.Errorf(
			codes.Internal,
			fmt.Sprintf("Can not convert to ObjectID: %v", insertErr),
		)
	}
	return &blogpb.CreateBlogResponse{
		Blog: &blogpb.Blog{
			Id:       blogID.Hex(),
			AuthorId: blog.GetAuthorId(),
			Title:    blog.GetTitle(),
			Content:  blog.GetContent(),
		},
	}, nil

}
func (*server) ReadBlog(ctx context.Context, req *blogpb.ReadBlogRequest) (*blogpb.ReadBlogResponse, error) {
	blogID := req.GetBlogId()
	oID, err := primitive.ObjectIDFromHex(blogID)
	if err != nil {
		return nil, status.Errorf(
			codes.InvalidArgument,
			fmt.Sprintf("Invalid blog ID :%v", err),
		)
	}
	data := &blogItem{}
	findFilter := bson.M{"_id": oID}
	findOpts := []*options.FindOneOptions{}
	res := collection.FindOne(context.Background(), findFilter, findOpts...)
	if err := res.Decode(data); err != nil {
		return nil, status.Errorf(
			codes.NotFound,
			fmt.Sprintf("Blog not found: %v", err),
		)
	}
	return &blogpb.ReadBlogResponse{
		Blog: blogItemToBlogpb(data),
	}, nil
}
func (*server) UpdateBlog(ctx context.Context, req *blogpb.UpdateBlogRequest) (*blogpb.UpdateBlogResponse, error) {
	blog := req.GetBlog()
	blogID, err := primitive.ObjectIDFromHex(blog.GetId())
	if err != nil {
		return nil, status.Errorf(
			codes.InvalidArgument,
			fmt.Sprintf("Can not parse blog ID: %v", err),
		)
	}
	data := &blogItem{
		AuthorID: blog.GetAuthorId(),
		Title:    blog.GetTitle(),
		Content:  blog.GetContent(),
	}
	replaceFilter := bson.M{"_id": blogID}
	replaceOpts := []*options.ReplaceOptions{}
	_, updateErr := collection.ReplaceOne(context.Background(), replaceFilter, data, replaceOpts...)
	if updateErr != nil {
		return nil, status.Errorf(
			codes.NotFound,
			fmt.Sprintf("Can not update blog : %v", updateErr),
		)
	}
	data.ID = blogID
	return &blogpb.UpdateBlogResponse{
		Blog: blogItemToBlogpb(data),
	}, nil
}
func blogItemToBlogpb(item *blogItem) *blogpb.Blog {
	return &blogpb.Blog{
		Id:       item.ID.Hex(),
		AuthorId: item.AuthorID,
		Title:    item.Title,
		Content:  item.Content,
	}
}
func (*server) DeleteBlog(ctx context.Context, req *blogpb.DeleteBlogRequest) (*blogpb.DeleteBlogResponse, error) {
	oID, parseErr := primitive.ObjectIDFromHex(req.GetBlogId())
	if parseErr != nil {
		return nil, status.Errorf(
			codes.InvalidArgument,
			fmt.Sprintf("Can not parse blog ID: %v", parseErr),
		)
	}
	deleteFilter := bson.M{"_id": oID}
	res, deleteErr := collection.DeleteOne(context.Background(), deleteFilter)
	if deleteErr != nil {
		return nil, status.Errorf(
			codes.Internal,
			fmt.Sprintf("Can not delete blog : %v", deleteErr),
		)
	}
	if res.DeletedCount == 0 {
		return nil, status.Errorf(
			codes.NotFound,
			fmt.Sprintf("Not found blog with provided ID"),
		)
	}
	return &blogpb.DeleteBlogResponse{
		BlogId: req.GetBlogId(),
	}, nil
}
func (*server) ListBlog(req *blogpb.ListBlogRequest, stream blogpb.BlogService_ListBlogServer) error {
	findFilter := primitive.D{{}}
	cur, err := collection.Find(context.Background(), findFilter)
	if err != nil {
		return status.Errorf(
			codes.Internal,
			fmt.Sprintf("internal error when find collection: %v", err),
		)
	}
	defer cur.Close(context.Background())
	for cur.Next(context.Background()) {
		data := &blogItem{}
		err := cur.Decode(data)
		if err != nil {
			return status.Errorf(
				codes.Internal,
				fmt.Sprintf("unable to decode data : %v", err),
			)
		}
		stream.Send(&blogpb.ListBlogResponse{
			Blog: blogItemToBlogpb(data),
		})
	}
	if err := cur.Err(); err != nil {
		return status.Errorf(
			codes.Internal,
			fmt.Sprintf("internal error from cursor: %v", err),
		)
	}
	return nil
}
