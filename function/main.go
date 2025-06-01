package main

import (
	"context"
	"encoding/json"
	"fmt"
	"function/models"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"google.golang.org/genai"
	"log"
	"net/http"
	"os"
	"time"
)

const (
	getAnalysisPath    = "GET /rb/ask/{examId}/{questionId}"
	createAnalysisPath = "POST /rb/ask"
)

var (
	askTableName string
	genaiApiKey  string

	dbClient    *dynamodb.Client
	genaiClient *genai.Client
)

func init() {
	askTableName = os.Getenv("ASK_TABLE_NAME")
	genaiApiKey = os.Getenv("GENAI_API_KEY")

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		panic(err)
	}

	dbClient = dynamodb.NewFromConfig(cfg)

	genaiClient, err = genai.NewClient(context.TODO(), &genai.ClientConfig{
		APIKey:  genaiApiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		panic(err)
	}
}

func handler(request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	path := request.RouteKey

	return func() (events.APIGatewayV2HTTPResponse, error) {
		switch path {
		case getAnalysisPath:
			return getAnalysis(request)
		case createAnalysisPath:
			return createAnalysis(request)
		default:
			return events.APIGatewayV2HTTPResponse{
				Body:       "Path Not Found",
				StatusCode: http.StatusNotFound,
			}, nil
		}
	}()
}

func getAnalysis(request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	pathParams := request.PathParameters
	examId, _ := pathParams["examId"]
	questionId, _ := pathParams["questionId"]

	builder := expression.Key("exam_question_key").Equal(expression.Value(fmt.Sprintf("%s#%s", examId, questionId)))
	expr, _ := expression.NewBuilder().WithKeyCondition(builder).Build()

	input := &dynamodb.QueryInput{
		TableName:                 aws.String(askTableName),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
	}

	result, err := dbClient.Query(context.TODO(), input)
	if err != nil {
		log.Println(fmt.Sprintf("Error getting analysis for %s#%s: %v", examId, questionId, err))
		return events.APIGatewayV2HTTPResponse{
			Body:       "Error getting bookmarks",
			StatusCode: http.StatusInternalServerError,
		}, nil
	}

	analysis := make(map[string]models.Analysis)
	for _, item := range result.Items {
		if model, ext := item["model"]; ext {
			analysis[model.(*types.AttributeValueMemberS).Value] = models.Analysis{
				Answer:      item["answer"].(*types.AttributeValueMemberS).Value,
				Explanation: item["explanation"].(*types.AttributeValueMemberS).Value,
			}
		}
	}

	response, _ := json.Marshal(analysis)

	return events.APIGatewayV2HTTPResponse{
		Body:       string(response),
		StatusCode: http.StatusOK,
	}, nil
}

func createAnalysis(request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	question := models.Question{}
	err := json.Unmarshal([]byte(request.Body), &question)
	if err != nil {
		log.Println(fmt.Sprintf("Error unmarshal question when creating analysis: %v", err))
		return events.APIGatewayV2HTTPResponse{
			Body:       "Error creating analysis",
			StatusCode: http.StatusBadRequest,
		}, nil
	}

	analysis, err := fetchAnalysis(question)
	if err != nil {
		log.Println(fmt.Sprintf("Error fetching analysis when creating analysis: %v", err))
		return events.APIGatewayV2HTTPResponse{
			Body:       "Error creating analysis",
			StatusCode: http.StatusBadRequest,
		}, nil
	}

	item := map[string]types.AttributeValue{
		"exam_question_key": &types.AttributeValueMemberS{
			Value: fmt.Sprintf("%s#%d", question.ExamId, question.QuestionId),
		},
		"model": &types.AttributeValueMemberS{
			Value: "gemini-2.0-flash-lite",
		},
		"answer": &types.AttributeValueMemberS{
			Value: analysis.Answer,
		},
		"explanation": &types.AttributeValueMemberS{
			Value: analysis.Explanation,
		},
		"created_at": &types.AttributeValueMemberS{
			Value: time.Now().UTC().Format(time.RFC3339),
		},
	}

	_, err = dbClient.PutItem(context.TODO(), &dynamodb.PutItemInput{
		TableName: aws.String(askTableName),
		Item:      item,
	})

	if err != nil {
		log.Println(fmt.Sprintf("Error putting analysis item in dynamodb: %v", err))
		return events.APIGatewayV2HTTPResponse{
			Body:       "Error creating analysis",
			StatusCode: http.StatusInternalServerError,
		}, nil
	}

	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusCreated,
	}, nil
}

func fetchAnalysis(question models.Question) (models.Analysis, error) {
	content := fmt.Sprintf("%s\n", question.Question)
	for _, option := range question.Options {
		content += fmt.Sprintf("%s\n", option.Text)
	}

	temperature := float32(0)
	analysisConfig := &genai.GenerateContentConfig{
		Temperature:      &temperature,
		ResponseMIMEType: "application/json",
		ResponseSchema: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"answers": {
					Type: genai.TypeArray,
					Items: &genai.Schema{
						Type: genai.TypeString,
					},
				},
				"explanation": {
					Type: genai.TypeString,
				},
			},
		},
	}

	result, err := genaiClient.Models.GenerateContent(
		context.Background(),
		"gemini-2.0-flash-lite",
		genai.Text(content),
		analysisConfig,
	)

	if err != nil {
		return models.Analysis{}, err
	}

	var analysis models.Analysis
	if err := json.Unmarshal([]byte(result.Text()), &analysis); err != nil {
		return models.Analysis{}, err
	}

	return analysis, nil
}

func main() {
	lambda.Start(handler)
}
